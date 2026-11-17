package journey

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func sdkRun(id, state, request, checkpoint string) store.Run {
	r := run(id, "node-workflow", "sdk-project", state, 0, nil)
	r.Options = store.JSON(`{"runtime":"codex-sdk","request":` + request + `}`)
	r.Doc = store.JSON(`{"sdk":` + checkpoint + `}`)
	return r
}

func TestSDKChecksDistinguishCompletionProgramAndModelReview(t *testing.T) {
	request := `{"workflow":"node-workflow","nodes":[{"id":"write","title":"Write","kind":"task"},{"id":"approve","kind":"approval"},{"id":"checked","title":"Measure","kind":"task","acceptance":"data-quality-profile"},{"id":"review","title":"Review","kind":"check"}]}`
	r := sdkRun("checked", "passed", request, `{"phase":"passed","nodes":[{"id":"write","phase":"passed","verification":"not_requested"},{"id":"approve","phase":"passed"},{"id":"checked","phase":"passed","verification":"passed"},{"id":"review","phase":"passed"}]}`)
	got := InspectRun(r)
	if len(got.Criteria) != 2 || !got.Criteria[0].Proven || got.Criteria[0].Basis != "program_check" || !got.Criteria[1].Proven || got.Criteria[1].Basis != "model_review" {
		t.Fatalf("evidence: %+v", got)
	}
	j := Build(Group([]store.Run{r})[0], Evidence{})
	if j.Criteria != 2 || j.Proven != 2 || j.Maturity != MaturityProvenOnce || !j.NeedsDecision {
		t.Fatalf("journey: %+v", j)
	}
	for _, checkpoint := range []string{
		`{"phase":"passed","nodes":[{"id":"checked","phase":"passed","verification":"not_requested"}]}`,
		`{"phase":"running","nodes":[{"id":"unrelated","phase":"passed","verification":"passed"}]}`,
		`{"phase":"running","nodes":[]}`,
	} {
		r.Doc = store.JSON(`{"sdk":` + checkpoint + `}`)
		got = InspectRun(r)
		if len(got.Criteria) != 2 || got.Criteria[0].Proven || got.Criteria[1].Proven {
			t.Fatalf("borrowed an undeclared/unverified pass: %+v", got)
		}
	}
}

func TestSDKUncheckedCompletionNeverBecomesAProofClaim(t *testing.T) {
	for _, request := range []string{`{"workflow":"checked-task"}`, `{"workflow":"node-workflow","nodes":[{"id":"write","kind":"task"},{"id":"summary","kind":"document"}]}`} {
		r := sdkRun("plain", "passed", request, `{"phase":"passed","verification":"not_requested","nodes":[{"id":"write","phase":"passed"},{"id":"summary","phase":"passed"}]}`)
		j := Build(Group([]store.Run{r})[0], Evidence{})
		if j.Criteria != 0 || j.Proven != 0 || j.Maturity != MaturityUnproven || !j.NeedsDecision {
			t.Fatalf("ordinary completion claimed proof: %+v", j)
		}
	}
	legacy := run("legacy", "old-graph", "p", "passed", 0, map[string]any{"criteria": []any{map[string]any{"id": "gate"}}})
	if got := InspectRun(legacy); len(got.Criteria) != 0 {
		t.Fatalf("reconstructed legacy proof: %+v", got)
	}
}

func TestSDKCurrentStepAndRehearsalRemainHonest(t *testing.T) {
	r := sdkRun("reviewing", "running", `{"workflow":"node-workflow","nodes":[{"id":"review","kind":"check"}]}`, `{"phase":"running","currentNode":"review","nodes":[{"id":"review","phase":"running"}]}`)
	if j := Build(Group([]store.Run{r})[0], Evidence{}); j.Stage != StageVerifying {
		t.Fatalf("review stage: %+v", j)
	}
	r.State = "failed"
	r.Doc = store.JSON(`{"sdk":{"phase":"failed","currentNode":"review","nodes":[{"id":"review","phase":"failed","findings":["The requested source is missing."]}]}}`)
	j := Build(Group([]store.Run{r})[0], Evidence{})
	if j.Attempts[0].FailingNode != "review" || j.Attempts[0].FailureReason != "The requested source is missing." || j.Proven != 0 {
		t.Fatalf("wrong stopped evidence: %+v", j)
	}
	r.Mode = "dry-run"
	r.State = "passed"
	r.Doc = store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"review","phase":"passed"}]}}`)
	j = Build(Group([]store.Run{r})[0], Evidence{})
	if j.Proven != 0 || j.Maturity != MaturityUnproven {
		t.Fatalf("rehearsal claimed proof: %+v", j)
	}
}

func continuedRun(id, source, root string, offset int) store.Run {
	r := run(id, "node-workflow", "sdk-project", "passed", offset, nil)
	r.ParentRun = source
	options := map[string]any{"runtime": "codex-sdk", "continuation": map[string]string{"sourceRunId": source, "rootRunId": root}, "request": map[string]string{"workflow": "node-workflow"}}
	raw, _ := json.Marshal(options)
	r.Options = raw
	return r
}

func TestSDKContinuationsStayVisibleAndDoNotInheritDecisions(t *testing.T) {
	source := sdkRun("source", "interrupted", `{"workflow":"node-workflow"}`, `{"phase":"interrupted"}`)
	child := continuedRun("child", source.RunID, source.RunID, -10)
	grandchild := continuedRun("grandchild", child.RunID, source.RunID, -20)
	buckets := Group([]store.Run{source, child, grandchild})
	if len(buckets) != 1 || len(buckets[0].Runs) != 3 || buckets[0].Runs[0].RunID != grandchild.RunID {
		t.Fatalf("continuation excluded or ordered by a backwards clock: %+v", buckets)
	}
	decision := store.Decision{ID: store.JourneyOutcomeDecisionID(buckets[0].Key), Decision: "accepted", Refs: []string{"run:source"}, CreatedAt: base}
	j := Build(buckets[0], Evidence{JourneyOutcome: &decision})
	if j.Outcome != nil || !j.NeedsDecision || j.Attempts[0].ContinuedFrom != child.RunID || j.Attempts[0].Superseded {
		t.Fatalf("old decision covered continued work: %+v", j)
	}
	// Immutable root metadata keeps the same delivery even after every ancestor
	// leaves the query window or is deleted. No source fetch is needed.
	only := Group([]store.Run{grandchild})
	if len(only) != 1 || only[0].Key != j.Key || only[0].Root != source.RunID {
		t.Fatalf("source deletion changed delivery: %+v", only)
	}
	unrelated := continuedRun("nested", "parent", "parent", 1)
	unrelated.Options = store.JSON(`{"runtime":"codex-sdk"}`)
	if got := Group([]store.Run{unrelated}); len(got) != 0 {
		t.Fatalf("ordinary nested work became a continuation: %+v", got)
	}
}

// The Inbox says it shows the whole failing finding. A 200-rune clip cut the
// first FAIL finding mid-sentence, so the card promised a reason it had already
// thrown away. The remaining bound is a transport one, and it announces itself.
func TestFailureReasonKeepsTheWholeFindingAndMarksARealCut(t *testing.T) {
	request := `{"workflow":"node-workflow","nodes":[{"id":"review","title":"Review","kind":"check"}]}`
	long := "FAIL — The revenue claim cites no source; " + strings.Repeat("the brief names a figure that appears nowhere in the delivered document. ", 6)
	finding, err := json.Marshal(strings.TrimSpace(long))
	if err != nil {
		t.Fatal(err)
	}
	r := sdkRun("clipped", "failed", request,
		`{"phase":"failed","currentNode":"review","nodes":[{"id":"review","phase":"failed","findings":[`+string(finding)+`]}]}`)
	got := InspectRun(r)
	want := strings.Join(strings.Fields(long), " ")
	if got.FailureReason != want {
		t.Fatalf("reason was clipped at %d bytes (want the whole %d-byte finding):\n%q", len(got.FailureReason), len(want), got.FailureReason)
	}
	if strings.HasSuffix(got.FailureReason, "…") {
		t.Errorf("a complete finding must not claim it was cut: %q", got.FailureReason)
	}

	// Only a genuinely enormous reason is shortened, and it then says so.
	huge, err := json.Marshal(strings.Repeat("é", maxFailureReason))
	if err != nil {
		t.Fatal(err)
	}
	r.Doc = store.JSON(`{"sdk":{"phase":"failed","currentNode":"review","nodes":[{"id":"review","phase":"failed","findings":[` + string(huge) + `]}]}}`)
	cut := InspectRun(r).FailureReason
	if !strings.HasSuffix(cut, "…") {
		t.Errorf("an actual cut must be marked: %q", cut[max(0, len(cut)-32):])
	}
	if len(cut) > maxFailureReason+len("…") {
		t.Errorf("reason exceeds its bound: %d bytes", len(cut))
	}
	if !utf8.ValidString(cut) {
		t.Error("the bound split a multi-byte rune")
	}
}
