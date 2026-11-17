package optimize

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// mockGenEngine is a hermetic fake for GenerateEvalset tests.
// It implements genEvalsetEngine and always returns fixedText / fixedJSON.
type mockGenEngine struct {
	fixedText string
	fixedJSON json.RawMessage
}

func (e *mockGenEngine) runRaw(_ context.Context, _ string) (string, json.RawMessage, error) {
	return e.fixedText, e.fixedJSON, nil
}

// fixedCasesJSON is the canned model response: 3 cases with distinct, harsh criteria.
const fixedCasesJSON = `[
  {
    "input": {"task": "summarize the quarterly earnings report", "format": "bullet points"},
    "criteria": "must identify the top 3 material financial metrics (revenue, margin, guidance) with specific figures — not just label them; a vague 'revenue increased' without numbers or context fails",
    "checks": [{"kind": "text-nonempty"}]
  },
  {
    "input": {"task": "extract action items from meeting transcript", "transcript": "Bob: we need to fix the auth bug by Friday. Alice: I'll update the README. Charlie: can someone review PR #42?"},
    "criteria": "must emit a structured list with owner, action, and deadline for every clearly stated action item in the transcript — missing any of the 3 items or omitting owners fails",
    "checks": [{"kind": "text-nonempty"}]
  },
  {
    "input": {"task": "rewrite the error message to be user-friendly", "original": "NullPointerException at line 42"},
    "criteria": "must replace all technical jargon with plain English, explain what went wrong and what the user can do next — keeping 'NullPointerException' or providing no actionable guidance fails",
    "checks": [{"kind": "text-nonempty"}]
  }
]`

// openTestStoreForGen opens the DB; skips if DATABASE_URL is absent (same
// pattern as the other hermetic tests in this package).
func openTestStoreForGen(t *testing.T) *store.Store {
	t.Helper()
	return openTestStore(t)
}

// ---------------------------------------------------------------------------
// parseGenCases unit tests (no DB needed)
// ---------------------------------------------------------------------------

func TestParseGenCasesFromJSON(t *testing.T) {
	var rawJSON json.RawMessage = json.RawMessage(fixedCasesJSON)
	cases, err := parseGenCases(rawJSON, "")
	if err != nil {
		t.Fatalf("parseGenCases: %v", err)
	}
	if len(cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(cases))
	}
	for i, c := range cases {
		if strings.TrimSpace(c.Criteria) == "" {
			t.Errorf("case %d: criteria is empty", i)
		}
		if len(c.Checks) == 0 {
			t.Errorf("case %d: checks is empty", i)
		}
	}
}

func TestParseGenCasesFromText(t *testing.T) {
	// Model returns free text with the JSON array embedded in prose.
	freeText := "Here are the eval cases:\n\n" + fixedCasesJSON + "\n\nDone."
	cases, err := parseGenCases(nil, freeText)
	if err != nil {
		t.Fatalf("parseGenCases from text: %v", err)
	}
	if len(cases) < 1 {
		t.Fatalf("expected >=1 cases, got 0")
	}
}

func TestParseGenCasesError(t *testing.T) {
	_, err := parseGenCases(nil, "no JSON here at all, just prose")
	if err == nil {
		t.Fatal("expected error for non-JSON text, got nil")
	}
}

// ---------------------------------------------------------------------------
// buildEvalset unit tests (no DB needed)
// ---------------------------------------------------------------------------

func TestBuildEvalsetShape(t *testing.T) {
	var rawCases []genCaseRaw
	if err := json.Unmarshal([]byte(fixedCasesJSON), &rawCases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	es := buildEvalset("my-skill", rawCases)

	if es.ID != "my-skill-eval" {
		t.Errorf("ID: want %q, got %q", "my-skill-eval", es.ID)
	}
	if es.SkillRef != "my-skill" {
		t.Errorf("SkillRef: want %q, got %q", "my-skill", es.SkillRef)
	}
	if len(es.Cases) != 3 {
		t.Fatalf("Cases: want 3, got %d", len(es.Cases))
	}
	for i, c := range es.Cases {
		if c.ID == "" {
			t.Errorf("case %d: empty ID", i)
		}
		if c.Criteria == "" {
			t.Errorf("case %d: empty criteria", i)
		}
		if len(c.Checks) == 0 {
			t.Errorf("case %d: no checks", i)
		}
		// Input must be valid JSON.
		if !json.Valid(c.Input) {
			t.Errorf("case %d: Input is not valid JSON: %s", i, c.Input)
		}
	}
}

func TestBuildEvalsetDefaultChecks(t *testing.T) {
	// A case with no checks should receive the text-nonempty default.
	rawCases := []genCaseRaw{
		{
			Input:    json.RawMessage(`{"foo":"bar"}`),
			Criteria: "output must be non-generic",
			Checks:   nil,
		},
	}
	es := buildEvalset("sk", rawCases)
	if len(es.Cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(es.Cases))
	}
	if len(es.Cases[0].Checks) == 0 {
		t.Error("expected default text-nonempty check, got none")
	}
	if es.Cases[0].Checks[0].Kind != "text-nonempty" {
		t.Errorf("default check kind: want text-nonempty, got %s", es.Cases[0].Checks[0].Kind)
	}
}

// ---------------------------------------------------------------------------
// GenerateEvalset integration test (DB required)
// ---------------------------------------------------------------------------

func TestGenerateEvalsetMockEngine(t *testing.T) {
	st := openTestStoreForGen(t)
	defer st.Close()
	ctx := context.Background()

	const skillID = "gen-evalset-test-skill"

	// Seed a skill so GetSkill succeeds.
	if err := st.UpsertSkill(ctx, store.Skill{
		ID:          skillID,
		Name:        "Test Skill for Evalset Gen",
		Instruction: "Given a document, extract and structure the key points.",
	}); err != nil {
		t.Fatalf("UpsertSkill: %v", err)
	}

	// Inject a mock engine that returns our fixed JSON.
	mockEng := &mockGenEngine{
		fixedText: fixedCasesJSON,
		fixedJSON: json.RawMessage(fixedCasesJSON),
	}

	// Temporarily patch generateEvalsetWithEngine — we call the internal helper
	// directly so we can inject the mock without touching the real engine.
	es, err := generateEvalsetWithEngine(ctx, st, skillID, mockEng)
	if err != nil {
		t.Fatalf("generateEvalsetWithEngine: %v", err)
	}

	// Assert shape.
	if len(es.Cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(es.Cases))
	}
	if es.SkillRef != skillID {
		t.Errorf("SkillRef: want %q, got %q", skillID, es.SkillRef)
	}
	for i, c := range es.Cases {
		if strings.TrimSpace(c.Criteria) == "" {
			t.Errorf("case %d: empty criteria", i)
		}
	}

	// Round-trip: verify it was persisted.
	got, err := st.GetEvalset(ctx, es.ID)
	if err != nil {
		t.Fatalf("GetEvalset round-trip: %v", err)
	}
	if len(got.Cases) != len(es.Cases) {
		t.Errorf("round-trip: case count mismatch: got %d, want %d", len(got.Cases), len(es.Cases))
	}
	if got.SkillRef != skillID {
		t.Errorf("round-trip: SkillRef: got %q, want %q", got.SkillRef, skillID)
	}
}
