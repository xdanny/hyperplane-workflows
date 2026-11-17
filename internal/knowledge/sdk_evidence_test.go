package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKExtractionDescribesAllSupportedWorkflows(t *testing.T) {
	for _, workflow := range []string{"node-workflow", "checked-task", "app-ideator"} {
		t.Run(workflow, func(t *testing.T) {
			run := sdkFixture("current", "passed", "run")
			if workflow != "node-workflow" {
				run.Options = store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"` + workflow + `","instruction":"Compare rival offerings"}}`)
				run.Doc = store.JSON(`{"sdk":{"phase":"passed","verification":"passed","findings":["every claim is cited"],"files":[{"path":"report.md","sha256":"recorded-hash"}]}}`)
			}
			snapshot, err := inspectSDKExtraction(run)
			if err != nil {
				t.Fatal(err)
			}
			prompt := buildExtractPrompt(run, snapshot, nil)
			for _, want := range []string{workflow, "every claim is cited", "Completion alone does not establish correctness", "Propose a draft for review", "fallible judgments"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("missing %q: %s", want, prompt)
				}
			}
			if workflow == "checked-task" && !strings.Contains(prompt, "No extra check or model review was requested") {
				t.Fatal("unchecked project task falsely verified")
			}
			if workflow == "node-workflow" && !strings.Contains(prompt, "model_review") {
				t.Fatal("review basis omitted")
			}
		})
	}
}

func TestSDKExtractionRejectsIncompleteOrContradictoryCheckpoint(t *testing.T) {
	cases := []struct{ name, options, doc string }{
		{"no SDK", `{}`, `{}`},
		{"incomplete", `{"runtime":"codex-sdk","request":{"workflow":"checked-task"}}`, `{"sdk":{"phase":"running"}}`},
		{"missing step", `{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"one","kind":"task"}]}}`, `{"sdk":{"phase":"passed","nodes":[]}}`},
		{"foreign step", `{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"one","kind":"task"}]}}`, `{"sdk":{"phase":"passed","nodes":[{"id":"foreign","phase":"passed"}]}}`},
		{"duplicate step", `{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"one","kind":"task"},{"id":"two","kind":"task"}]}}`, `{"sdk":{"phase":"passed","nodes":[{"id":"one","phase":"passed"},{"id":"one","phase":"passed"}]}}`},
		{"unverified check", `{"runtime":"codex-sdk","request":{"workflow":"checked-task","check":["sh","checks.sh"]}}`, `{"sdk":{"phase":"passed","verification":"failed"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inspectSDKExtraction(store.Run{Options: store.JSON(tc.options), Doc: store.JSON(tc.doc)})
			if !errors.Is(err, ErrRunNotEligible) {
				t.Fatalf("accepted malformed/incomplete evidence: %v", err)
			}
		})
	}
}

func TestSDKExtractionDoesNotBorrowContinuationAcceptanceOrEvidence(t *testing.T) {
	st := testStore(t)
	sourceID, childID := uniqueID("source"), uniqueID("continuation")
	seedRun(t, st, sourceID, "interrupted", "run")
	acceptRun(t, st, sourceID, "accepted")
	child := sdkFixture(childID, "passed", "run")
	child.ParentRun = sourceID
	var options map[string]any
	if err := json.Unmarshal(child.Options, &options); err != nil {
		t.Fatal(err)
	}
	options["continuation"] = map[string]string{"sourceRunId": sourceID, "rootRunId": sourceID}
	options["request"].(map[string]any)["continuation"] = map[string]any{"checkpoint": map[string]any{"findings": []string{"SOURCE SECRET CONCLUSION"}}}
	child.Options, _ = json.Marshal(options)
	if err := st.CreateRun(t.Context(), child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), childID) })
	called := false
	engine := func(_ context.Context, o codex.Options) (codex.Result, error) {
		called = true
		if strings.Contains(o.Instruction, "SOURCE SECRET") {
			t.Fatal("continuation seed was treated as current observations")
		}
		proposal := sampleProposal(uniqueID("current-method"), "Current method")
		proposal["evidence"] = []Evidence{{RunID: sourceID, NodeID: "gather", Quote: "three rivals, all subscription-priced"}, {NodeID: "gather", Quote: "invented favorable quote"}, {NodeID: "verify", Quote: "every claim is cited"}}
		raw, _ := json.Marshal(proposal)
		return codex.Result{OK: true, JSON: raw}, nil
	}
	if _, err := ExtractSkill(t.Context(), st, Extractor{Codex: engine}, childID); !errors.Is(err, ErrRunNotEligible) || called {
		t.Fatalf("source acceptance authorized child extraction: err=%v called=%v", err, called)
	}
	acceptRun(t, st, childID, "accepted")
	result, err := ExtractSkill(t.Context(), st, Extractor{Codex: engine}, childID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill == nil {
		t.Fatalf("no draft: %+v", result)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), result.Skill.ID) })
	if len(result.Proposal.Evidence) != 1 || result.Proposal.Evidence[0].RunID != childID || result.Proposal.Evidence[0].NodeID != "verify" {
		t.Fatalf("untrusted/foreign quotes pinned: %+v", result.Proposal.Evidence)
	}
	if !store.IsDraftSkill(*result.Skill) {
		t.Fatal("accepted source promoted extracted method automatically")
	}
}

func TestExtractSkillFromAcceptedDirectSDKTaskAndIdeator(t *testing.T) {
	for _, workflow := range []string{"checked-task", "app-ideator"} {
		t.Run(workflow, func(t *testing.T) {
			st := testStore(t)
			runID, skillID := uniqueID("sdk-direct"), uniqueID("sdk-method")
			run := store.Run{RunID: runID, DAG: "sdk-direct", State: "passed", Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"` + workflow + `","instruction":"Compare offerings"}}`), Doc: store.JSON(`{"sdk":{"phase":"passed","verification":"passed","findings":["every claim is cited"]}}`)}
			if err := st.CreateRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = st.DeleteRun(context.Background(), runID)
				_ = st.DeleteSkill(context.Background(), skillID)
			})
			acceptRun(t, st, runID, "accepted")
			proposal := sampleProposal(skillID, "Direct SDK method")
			proposal["evidence"] = []Evidence{{NodeID: "delivery", Quote: "every claim is cited"}}
			res, err := ExtractSkill(t.Context(), st, Extractor{Codex: fakeCodex(t, proposal)}, runID)
			if err != nil {
				t.Fatal(err)
			}
			if res.Skill == nil || !store.IsDraftSkill(*res.Skill) || len(res.Proposal.Evidence) != 1 || res.Proposal.Evidence[0].RunID != runID {
				t.Fatalf("direct SDK extraction lost draft/evidence: %+v", res)
			}
		})
	}
}

func TestSDKCheckpointExcerptPrioritizesMethodOverLargeInventory(t *testing.T) {
	files := []map[string]string{{"path": "../foreign-report.md", "sha256": strings.Repeat("a", 64)}}
	for i := 0; i < 80; i++ {
		files = append(files, map[string]string{"path": fmt.Sprintf("outputs/artifact-%03d.json", i), "sha256": strings.Repeat("b", 64)})
	}
	observedText := "Fixed comparison axes before collecting candidate results."
	observedFinding := "Rejected uncited claims before writing the comparison."
	raw, err := json.Marshal(map[string]any{"id": "gather", "phase": "passed", "verification": "not_requested", "files": files, "text": observedText, "findings": []string{observedFinding}, "instruction": "UNOBSERVED REQUEST MUST NOT BECOME EVIDENCE"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(raw), observedText) < maxOutputPreview {
		t.Fatal("fixture does not reproduce early inventory crowding")
	}
	for _, workflow := range []string{"node-workflow", "checked-task"} {
		t.Run(workflow, func(t *testing.T) {
			options := `{"runtime":"codex-sdk","request":{"workflow":"` + workflow + `","instruction":"REQUEST ONLY","nodes":[{"id":"gather","kind":"task"}]}}`
			doc := `{"sdk":` + string(raw) + `}`
			if workflow == "node-workflow" {
				doc = `{"sdk":{"phase":"passed","nodes":[` + string(raw) + `]}}`
			}
			run := store.Run{RunID: "current", Options: store.JSON(options), Doc: store.JSON(doc)}
			snapshot, err := inspectSDKExtraction(run)
			if err != nil {
				t.Fatal(err)
			}
			excerpt := snapshot.Nodes[0].Checkpoint
			for _, want := range []string{"phase: passed", "verification: not_requested", observedText, observedFinding, "outputs/artifact-000.json"} {
				if !strings.Contains(excerpt, want) {
					t.Fatalf("priority observation/reference missing %q: %s", want, excerpt)
				}
			}
			if len(excerpt) > maxOutputPreview+len("…") {
				t.Fatalf("excerpt unbounded: %d", len(excerpt))
			}
			for _, forbidden := range []string{"../foreign-report", "UNOBSERVED REQUEST", "REQUEST ONLY", "artifact-079.json"} {
				if strings.Contains(excerpt, forbidden) {
					t.Fatalf("untrusted/unbounded data %q in observation excerpt", forbidden)
				}
			}
			quoted := pinEvidence([]Evidence{{NodeID: snapshot.Nodes[0].ID, Quote: observedText}, {NodeID: snapshot.Nodes[0].ID, Quote: observedFinding}, {NodeID: snapshot.Nodes[0].ID, Quote: "REQUEST ONLY"}, {RunID: "foreign", NodeID: snapshot.Nodes[0].ID, Quote: observedText}}, run.RunID, snapshot.Nodes, nil)
			if len(quoted) != 2 {
				t.Fatalf("displayed method excerpts not pinned exclusively: %+v", quoted)
			}
		})
	}
}

func TestSDKCheckpointExcerptPreservesFindingsWhenTextIsLong(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"phase": "passed", "text": strings.Repeat("long narrative ", 400), "findings": []string{"Independent finding remains visible"}})
	excerpt, err := checkpointExcerpt(raw)
	if err != nil || !strings.Contains(excerpt, "Independent finding remains visible") || len(excerpt) > maxOutputPreview+len("…") {
		t.Fatalf("text crowded findings: %s (%v)", excerpt, err)
	}
	for _, raw := range []string{`{"phase":"passed","text":{"invented":"object"}}`, `{"phase":"passed","findings":"not an array"}`, `{"phase":"passed","files":[{"path":42}]}`} {
		if _, err := checkpointExcerpt(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted malformed observations: %s", raw)
		}
	}
}
