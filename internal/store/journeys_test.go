package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestJourneyOutcomeDecisionIDIsNamespaced asserts the two grains of outcome id
// cannot collide: run ids always start with a timestamp, journey keys never do.
func TestJourneyOutcomeDecisionIDIsNamespaced(t *testing.T) {
	key := "sample-journey-id"
	got := JourneyOutcomeDecisionID(key)
	if want := "outcome-journey-" + key; got != want {
		t.Errorf("JourneyOutcomeDecisionID = %q, want %q", got, want)
	}
	if got == OutcomeDecisionID(key) {
		t.Error("journey and run outcome ids must not coincide")
	}
}

// TestDecisionsByIDs asserts the batched lookup returns exactly the ids that
// exist — the inbox's "which of these have been decided?" in one query.
func TestDecisionsByIDs(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "journey-batch-" + uniq()
	if _, err := st.CreateDecision(ctx, Decision{ID: id, Title: "t", Decision: "accepted"}); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(ctx, id) })

	got, err := st.DecisionsByIDs(ctx, []string{id, "journey-batch-missing-" + uniq()})
	if err != nil {
		t.Fatalf("decisions by ids: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d decisions, want 1", len(got))
	}
	if got[id].Decision != "accepted" {
		t.Errorf("decision = %q, want accepted", got[id].Decision)
	}

	empty, err := st.DecisionsByIDs(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty lookup: %v / %d rows", err, len(empty))
	}
}

// SDK continuations are delivery attempts, while ordinary nested runs remain
// implementation details even when they happen to use the SDK runtime.
func TestListRunsTopLevelIncludesOnlyBoundSDKContinuations(t *testing.T) {
	st := testStore(t)
	t.Cleanup(st.Close)
	ctx := t.Context()
	project := "sdk-delivery-filter-" + uniq()
	root := project + "-root"
	child := project + "-continued"
	options := func(runtime, source string) JSON {
		value, _ := json.Marshal(map[string]any{"runtime": runtime, "continuation": map[string]string{"sourceRunId": source}})
		return value
	}
	fixtures := []struct {
		run  Run
		want bool
	}{
		{Run{RunID: root, State: "interrupted", Options: options("codex-sdk", "")}, true},
		{Run{RunID: child, ParentRun: root, State: "interrupted", Options: options("codex-sdk", root)}, true},
		{Run{RunID: project + "-continued-again", ParentRun: child, State: "passed", Options: options("codex-sdk", child)}, true},
		{Run{RunID: project + "-nested", ParentRun: root, State: "passed", Options: options("codex-sdk", "")}, false},
		{Run{RunID: project + "-wrong-source", ParentRun: root, State: "passed", Options: options("codex-sdk", child)}, false},
		{Run{RunID: project + "-wrong-runtime", ParentRun: root, State: "passed", Options: options("legacy", root)}, false},
	}
	for _, fixture := range fixtures {
		run := fixture.run
		run.Project = project
		run.DAG = "node-workflow"
		if err := st.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), run.RunID) })
	}
	all, err := st.ListRuns(ctx, RunFilter{Project: &project})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(fixtures) {
		t.Fatalf("unfiltered run count=%d", len(all))
	}
	top, err := st.ListRuns(ctx, RunFilter{Project: &project, TopLevel: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, run := range top {
		seen[run.RunID] = true
	}
	for _, fixture := range fixtures {
		if seen[fixture.run.RunID] != fixture.want {
			t.Errorf("top-level inclusion %s=%v want%v", fixture.run.RunID, seen[fixture.run.RunID], fixture.want)
		}
	}
	if len(top) != 3 {
		t.Fatalf("top-level runs=%d want3", len(top))
	}
}

func TestDeliveryDecisionDoesNotFlowFromSourceIntoContinuation(t *testing.T) {
	st := testStore(t)
	t.Cleanup(st.Close)
	ctx := t.Context()
	source := "sdk-outcome-source-" + uniq()
	child := source + "-continued"
	opts, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "continuation": map[string]string{"sourceRunId": source}})
	for _, run := range []Run{
		{RunID: source, DAG: "node-workflow", State: "interrupted", Options: JSON(`{"runtime":"codex-sdk"}`)},
		{RunID: child, DAG: "node-workflow", State: "passed", ParentRun: source, Options: opts},
	} {
		if err := st.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), run.RunID) })
	}
	oldID := JourneyOutcomeDecisionID("j-" + uniq())
	if _, err := st.CreateDecision(ctx, Decision{ID: oldID, Title: "Original delivery", Decision: "changes_requested", Tags: []string{OutcomeTag, "journey"}, Refs: []string{"run:" + source}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), oldID) })
	if got, err := st.DeliveryDecisionForRun(ctx, source); err != nil || got.ID != oldID {
		t.Fatalf("original decision lost: %+v %v", got, err)
	}
	if got, err := st.DeliveryDecisionForRun(ctx, child); !errors.Is(err, ErrNotFound) {
		t.Fatalf("continuation inherited source decision: %+v %v", got, err)
	}
	childID := OutcomeDecisionID(child)
	if _, err := st.CreateDecision(ctx, Decision{ID: childID, Title: "Continued delivery", Decision: "accepted", Tags: []string{OutcomeTag}, Refs: []string{"run:" + child}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), childID) })
	if got, err := st.DeliveryDecisionForRun(ctx, child); err != nil || got.ID != childID {
		t.Fatalf("continued decision lost: %+v %v", got, err)
	}
	if got, err := st.DeliveryDecisionForRun(ctx, source); err != nil || got.ID != oldID {
		t.Fatalf("source decision changed: %+v %v", got, err)
	}
}

func TestJourneyAttemptOutcomesAreDistinctImmutableAndPinned(t *testing.T) {
	st := testStore(t)
	t.Cleanup(st.Close)
	ctx := t.Context()
	key := "j-" + uniq()
	source := "journey-source-" + uniq()
	child := source + "-continued"
	future := child + "-again"
	for i, id := range []string{source, child, future} {
		parent := ""
		if i > 0 {
			parent = []string{source, child}[i-1]
		}
		options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "continuation": map[string]string{"sourceRunId": parent}})
		if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", State: "interrupted", ParentRun: parent, Options: options}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), id) })
	}
	firstID := JourneyAttemptOutcomeDecisionID(key, source)
	secondID := JourneyAttemptOutcomeDecisionID(key, child)
	if firstID != JourneyOutcomeDecisionID(key)+":"+source || firstID == secondID || firstID == OutcomeDecisionID(source) || firstID == JourneyOutcomeDecisionID(key) {
		t.Fatalf("invalid attempt outcome namespace: %q / %q", firstID, secondID)
	}
	for _, decision := range []Decision{
		{ID: firstID, Title: "Original result", Decision: "changes_requested", Why: "Needs the remaining work", Tags: []string{OutcomeTag, "journey"}, Refs: []string{"journey:" + key, "run:" + source}},
		{ID: secondID, Title: "Continued result", Decision: "rejected", Why: "The new result needs reconsideration", Tags: []string{OutcomeTag, "journey"}, Refs: []string{"journey:" + key, "run:" + child}},
	} {
		if _, err := st.CreateDecision(ctx, decision); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), decision.ID) })
	}
	if _, err := st.CreateDecision(ctx, Decision{ID: firstID, Title: "Overwrite", Decision: "accepted"}); err == nil {
		t.Fatal("recording the same attempt twice must not replace its decision")
	}
	stored, err := st.DecisionsByIDs(ctx, []string{firstID, secondID, JourneyOutcomeDecisionID(key)})
	if err != nil || len(stored) != 2 || stored[firstID].Decision != "changes_requested" || stored[firstID].Why != "Needs the remaining work" || stored[secondID].Decision != "rejected" {
		t.Fatalf("later outcome overwrote or hid the original: %+v %v", stored, err)
	}
	for runID, wantID := range map[string]string{source: firstID, child: secondID} {
		got, err := st.DeliveryDecisionForRun(ctx, runID)
		if err != nil || got.ID != wantID {
			t.Errorf("decision for %s = %+v %v; want %s", runID, got, err, wantID)
		}
	}
	if got, err := st.DeliveryDecisionForRun(ctx, future); !errors.Is(err, ErrNotFound) {
		t.Fatalf("later continuation inherited an outcome outside its refs: %+v %v", got, err)
	}
}
