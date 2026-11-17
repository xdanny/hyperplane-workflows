package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func sdkEvidenceFixture(id string) store.Run {
	return store.Run{RunID: id, DAG: "node-workflow", State: "passed",
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"Keep the declared measurements","nodes":[{"id":"write","title":"Create files","kind":"task","instruction":"Write the output"},{"id":"checked","title":"Check arithmetic","kind":"task","check":["sh","checks.sh"]},{"id":"unverified","title":"Check pending","kind":"task","acceptance":"prediction-metrics"},{"id":"review","title":"Review meaning","kind":"check"}]}}`),
		Doc:     store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"write","phase":"passed","verification":"passed"},{"id":"checked","phase":"passed","verification":"passed","findings":["Four measured rows agree"]},{"id":"unverified","phase":"passed","verification":"pending"},{"id":"review","phase":"passed","findings":["The interpretation is supported within the stated limits"]}]}}`)}
}

func evidenceText(ev Evidence) string {
	var text strings.Builder
	text.WriteString(ev.Note)
	for _, section := range ev.Sections {
		text.WriteString("\n" + section.Heading + "\n" + section.Body)
	}
	return text.String()
}

func TestSDKRunReviewSeparatesCompletionProgramChecksAndModelReview(t *testing.T) {
	run := sdkEvidenceFixture("current")
	got := evidenceText(RunEvidence(RunFacts{Run: run}))
	for _, want := range []string{
		"Frozen instruction: Keep the declared measurements",
		"step write (Create files): phase=passed; no configured check",
		"step checked (Check arithmetic): phase=passed; deterministic check; check passed=true",
		"step unverified (Check pending): phase=passed; deterministic check; check passed=false",
		"step review (Review meaning): phase=passed; model review (fallible); check passed=true",
		"Four measured rows agree", "The interpretation is supported within the stated limits",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, unwanted := range []string{"sub-run=", "score=", "GATE VERDICTS", "verdict-bearing"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("obsolete graph evidence %q", unwanted)
		}
	}
	run.Mode = "plan"
	if got := evidenceText(RunEvidence(RunFacts{Run: run})); !strings.Contains(got, "REHEARSAL") {
		t.Fatal("review must disclose simulated execution")
	}
}

func TestSkillSupportUsesOnlyItsExactSourceSDKCheckpoint(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()
	source := fmt.Sprintf("persona-source-%d", time.Now().UnixNano())
	run := sdkEvidenceFixture(source)
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), source) })
	child := source + "-continued"
	opts, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "continuation": map[string]string{"sourceRunId": source}, "request": map[string]any{"workflow": "node-workflow", "nodes": []map[string]string{{"id": "later-review", "title": "Later review", "kind": "check"}}}})
	if err := st.CreateRun(ctx, store.Run{RunID: child, ParentRun: source, DAG: "node-workflow", State: "passed", Options: opts, Doc: store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"later-review","phase":"passed","findings":["DESCENDANT CLAIM MUST NOT APPEAR"]}]}}`)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), child) })
	if err := st.UpsertNode(ctx, store.Node{RunID: source, NodeID: "old-gate", Kind: "gate", State: "passed", Output: store.JSON(`{"pass":true,"reasons":"GRAPH CLAIM MUST NOT APPEAR"}`)}); err != nil {
		t.Fatal(err)
	}
	skillID := source + "-skill"
	metadata, _ := json.Marshal(map[string]any{"sourceRuns": []string{source}, "evidence": []map[string]string{{"runId": source, "nodeId": "checked", "quote": "Four measured rows agree"}}})
	if err := st.UpsertSkill(ctx, store.Skill{ID: skillID, Name: "Measured method", Instruction: "Keep each declared comparison exact.", Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })
	ev, err := SkillEvidence(ctx, st, skillID)
	if err != nil {
		t.Fatal(err)
	}
	got := evidenceText(ev)
	for _, want := range []string{source, "SDK EVIDENCE OF THE SUPPORTING RUNS", "no configured check", "deterministic check; check passed=true", "model review (fallible)", "Four measured rows agree"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, absent := range []string{child, "old-gate", "GRAPH CLAIM MUST NOT APPEAR", "DESCENDANT CLAIM MUST NOT APPEAR"} {
		if strings.Contains(got, absent) {
			t.Errorf("source evidence contaminated by %q", absent)
		}
	}
}

func TestSDKReviewMissingOrDuplicateCheckpointDoesNotClaimVerification(t *testing.T) {
	for _, run := range []store.Run{
		{Options: store.JSON(`{"runtime":"legacy"}`), Doc: store.JSON(`{"sdk":{"phase":"passed"}}`)},
		{Options: store.JSON(`{"runtime":"codex-sdk"}`), Doc: store.JSON(`{}`)},
	} {
		if got := sdkRunSummary(run); !strings.Contains(got, "No recorded SDK step evidence") {
			t.Fatalf("missing SDK provenance must not claim verification: %s", got)
		}
	}
	run := sdkEvidenceFixture("duplicate")
	run.Doc = store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"checked","phase":"passed","verification":"passed"},{"id":"checked","phase":"passed","verification":"passed"}]}}`)
	if got := sdkRunSummary(run); !strings.Contains(got, "Duplicate SDK checkpoint") || strings.Contains(got, "check passed=true") {
		t.Fatalf("duplicate evidence must not verify anything: %s", got)
	}
}
