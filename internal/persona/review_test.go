package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed persona test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

const testCharter = `# Testy — senior reviewer

## IDENTITY

You are **Testy**, a senior reviewer with a long memory.

Your bar: **"Is this evidence, or is it theater?"**

## RULES

1. **Browser only.** Audit as a user.
2. **Exact quotes required.**

## AUDIT PROTOCOL

1. Establish custody of every number.

## COMPETENCE

You review, and are qualified to judge:

- **Skills**: ` + "`ai-engineering`" + `.
- **Recipes and processes**: ` + "`ml-engineering`" + `.

## SKILL-REVIEW PROTOCOL

Read the instruction and the pinned evidence. Verdict: APPROVE, REJECT, REVISE.
`

// fakeCodex returns a canned review and captures the prompt it was handed.
func fakeCodex(out map[string]any, seen *string) CodexFunc {
	return func(_ context.Context, o codex.Options) (codex.Result, error) {
		if seen != nil {
			*seen = o.Instruction
		}
		b, _ := json.Marshal(out)
		return codex.Result{OK: true, Status: 0, Text: string(b), JSON: b, Sandbox: o.SandboxMode}, nil
	}
}

func mustCharter(t *testing.T) Charter {
	t.Helper()
	c, err := Parse("testy.md", testCharter)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestBuildPromptCarriesCharterAndEvidence is the assembly contract: a review
// prompt is the charter's OWN sections plus the platform's evidence, so an
// invoked persona reads the same brief a hand-spawned one does.
func TestBuildPromptCarriesCharterAndEvidence(t *testing.T) {
	c := mustCharter(t)
	ev := Evidence{
		Target: Target{Kind: TargetSkill, ID: "ai-engineering"},
		Title:  "AI engineering (DRAFT)",
		Note:   "You are reading the skill as the platform stores it.",
		Sections: []Section{
			{Heading: "INSTRUCTION", Body: "Report the worst slice, not only the average."},
			{Heading: "PINNED EVIDENCE QUOTES", Body: `1. run r1, node gate: "each is tied to a quotation"`},
		},
	}
	p := BuildPrompt(c, ev)

	for _, want := range []string{
		"You are **Testy**",                             // IDENTITY, verbatim
		"Exact quotes required",                         // RULES, verbatim
		"Verdict: APPROVE, REJECT, REVISE",              // the SKILL-REVIEW PROTOCOL
		"skill:ai-engineering",                          // which target
		"Report the worst slice, not only the average.", // the evidence itself
		"each is tied to a quotation",
		"ADVISORY",                            // the rule that overrides the charter
		"Is this evidence, or is it theater?", // their bar
		"You have no browser, no shell",       // the honest boundary
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// A SKILL review must not be handed the run-review framing, and vice versa.
	if strings.Contains(p, "APPLIED TO ONE RUN") {
		t.Error("a skill target must not get the run-review framing")
	}

	runPrompt := BuildPrompt(c, Evidence{Target: Target{Kind: TargetRun, ID: "r1"}})
	if !strings.Contains(runPrompt, "APPLIED TO ONE RUN") {
		t.Error("a run target must get the AUDIT PROTOCOL, reframed for one run")
	}
	if !strings.Contains(runPrompt, "Establish custody of every number.") {
		t.Error("a run target must carry the charter's own audit protocol")
	}
}

// TestRunRecordsAdvisoryVerdict walks one review end to end with a faked engine
// and asserts what gets persisted: a record, insert-only, carrying the verdict
// and marked advisory.
func TestRunRecordsAdvisoryVerdict(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	c := mustCharter(t)
	target := Target{Kind: TargetSkill, ID: fmt.Sprintf("skill-%d", time.Now().UnixNano())}

	var prompt string
	cfg := Config{Codex: fakeCodex(map[string]any{
		"verdict":        "revise",
		"summary":        "The back half outruns its evidence.",
		"reasons":        []string{"Invented criteria."},
		"citations":      []map[string]string{{"ref": "INSTRUCTION", "quote": "rank the problems"}},
		"revisedWording": "Investigate from several distinct perspectives.",
	}, &prompt)}

	rev, err := Run(ctx, st, cfg, c, Evidence{Target: target, Title: "a skill"}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, rev.ID) })

	if rev.Verdict != VerdictRevise {
		t.Errorf("verdict = %q", rev.Verdict)
	}
	if !rev.Advisory {
		t.Error("every review is advisory — the flag must be set on the record")
	}
	if rev.RevisedWording == "" {
		t.Error("a revise verdict must carry the replacement wording")
	}
	if rev.CharterVersion != c.Version {
		t.Errorf("charterVersion = %q, want the charter hash %q", rev.CharterVersion, c.Version)
	}
	if prompt == "" {
		t.Fatal("the engine was never handed a prompt")
	}

	rec, err := st.GetRecord(ctx, store.PersonaReviewRecordType, rev.ID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if rec.State != VerdictRevise {
		t.Errorf("record state = %q, want the verdict", rec.State)
	}
	if !rec.Consumed {
		t.Error("a review is evidence, not a question — it must never queue in the inbox")
	}
	stored, err := ParseReview(rec.Doc)
	if err != nil || stored.Summary != rev.Summary {
		t.Errorf("the record body must round-trip the review: %v / %q", err, stored.Summary)
	}
}

// TestReviewsAreInsertOnlyAndRetained is the AI-12 rule: a second review never
// overwrites the first. Two personas disagreeing, or one persona re-reading
// after a change, is exactly the evidence a human wants both halves of.
func TestReviewsAreInsertOnlyAndRetained(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	c := mustCharter(t)
	target := Target{Kind: TargetSkill, ID: fmt.Sprintf("retained-%d", time.Now().UnixNano())}
	ev := Evidence{Target: target, Title: "a skill"}

	first, err := Run(ctx, st, Config{Codex: fakeCodex(map[string]any{
		"verdict": "revise", "summary": "Hold it.", "reasons": []string{"support = 1"},
	}, nil)}, c, ev, Options{})
	if err != nil {
		t.Fatalf("first review: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, first.ID) })

	second, err := Run(ctx, st, Config{Codex: fakeCodex(map[string]any{
		"verdict": "approve", "summary": "A second run now supports it.",
	}, nil)}, c, ev, Options{})
	if err != nil {
		t.Fatalf("second review: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, second.ID) })

	if first.ID == second.ID {
		t.Fatal("a second review must get its own id, not overwrite the first")
	}
	if want := ReviewID(c.ID, target, 2); second.ID != want {
		t.Errorf("second id = %q, want %q", second.ID, want)
	}

	reviews, err := st.ListPersonaReviews(ctx, target.String(), 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("both reviews must be retained, got %d", len(reviews))
	}
	// The first one is still readable, with its original verdict intact.
	old, err := st.GetRecord(ctx, store.PersonaReviewRecordType, first.ID)
	if err != nil || old.State != VerdictRevise {
		t.Errorf("the earlier verdict must survive: %v / %q", err, old.State)
	}
}

// TestRunRefusesAnInventedVerdict guards the record against a verdict the
// platform does not recognise. Storing "probably fine" as a state would make
// every downstream chip lie about what a reviewer said.
func TestRunRefusesAnInventedVerdict(t *testing.T) {
	st := testStore(t)
	c := mustCharter(t)
	target := Target{Kind: TargetSkill, ID: fmt.Sprintf("bad-%d", time.Now().UnixNano())}
	_, err := Run(context.Background(), st, Config{Codex: fakeCodex(map[string]any{
		"verdict": "probably fine", "summary": "eh",
	}, nil)}, c, Evidence{Target: target}, Options{})
	if err == nil {
		t.Fatal("an unrecognised verdict must be refused, not recorded")
	}
	if !strings.Contains(err.Error(), "not a verdict") {
		t.Errorf("the refusal should say why: %v", err)
	}
}

// TestReviewRunsReadOnly pins the sandbox posture. A reviewer READS; a persona
// that could write would be able to change the thing it is judging.
func TestReviewRunsReadOnly(t *testing.T) {
	st := testStore(t)
	c := mustCharter(t)
	target := Target{Kind: TargetSkill, ID: fmt.Sprintf("sandbox-%d", time.Now().UnixNano())}

	var sandbox string
	cfg := Config{Codex: func(_ context.Context, o codex.Options) (codex.Result, error) {
		sandbox = o.SandboxMode
		b := []byte(`{"verdict":"approve","summary":"fine","reasons":[],"citations":[],"revisedWording":""}`)
		return codex.Result{OK: true, Text: string(b), JSON: b}, nil
	}}
	rev, err := Run(context.Background(), st, cfg, c, Evidence{Target: target}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, rev.ID) })
	if sandbox != codex.SandboxReadOnly {
		t.Errorf("sandbox = %q, want %q", sandbox, codex.SandboxReadOnly)
	}
}

// TestModelOverrideSelectsTheLocalEngine covers the --model contract, including
// the local-engine naming the worker pool already uses.
func TestModelOverrideSelectsTheLocalEngine(t *testing.T) {
	cases := []struct {
		in      string
		wantOSS bool
		wantMod string
	}{
		{"", false, ""},
		{"gpt-5-codex", false, "gpt-5-codex"},
		{"local", true, ""},
		{"codex-local", true, ""},
		{"local:qwen3-coder", true, "qwen3-coder"},
	}
	for _, c := range cases {
		var o codex.Options
		applyModel(&o, c.in)
		if o.OSS != c.wantOSS || o.Model != c.wantMod {
			t.Errorf("applyModel(%q) = {OSS:%v Model:%q}, want {OSS:%v Model:%q}",
				c.in, o.OSS, o.Model, c.wantOSS, c.wantMod)
		}
	}
}
