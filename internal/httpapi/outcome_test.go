package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// postOutcome posts an Outcome Decision and returns the status + raw body.
func postOutcome(t *testing.T, url, verdict, note string) (int, []byte) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"verdict": verdict, "note": note})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, buf.Bytes()
}

// seedTerminalRun creates a finished run and schedules cleanup of both the run
// and any outcome decision recorded against it.
func seedTerminalRun(t *testing.T, st *store.Store, state string) string {
	t.Helper()
	runID := fmt.Sprintf("run-outcome-%d", time.Now().UnixNano())
	run := store.Run{RunID: runID, DAG: "hello", Goal: "ship the thing", State: state, Project: "hyperplane"}
	if err := st.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = st.DeleteDecision(ctx, store.OutcomeDecisionID(runID))
		_ = st.DeleteRun(ctx, runID)
	})
	return runID
}

// TestRecordOutcomeThenConflict is the core contract: the first verdict is
// recorded (201) and a second POST is refused (409) with the existing outcome
// echoed back. An outcome is final — changing it requires deleting the decision.
func TestRecordOutcomeThenConflict(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")
	url := ts.URL + "/api/runs/" + runID + "/outcome"

	status, body := postOutcome(t, url, "accepted", "shipped it")
	if status != http.StatusCreated {
		t.Fatalf("first outcome: expected 201, got %d: %s", status, body)
	}
	var first struct {
		RunID      string `json:"runId"`
		Verdict    string `json:"verdict"`
		Note       string `json:"note"`
		DecidedBy  string `json:"decidedBy"`
		DecisionID string `json:"decisionId"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("decode outcome: %v", err)
	}
	if first.Verdict != "accepted" || first.Note != "shipped it" || first.RunID != runID {
		t.Errorf("unexpected outcome: %+v", first)
	}
	if first.DecidedBy != "local" {
		t.Errorf("expected decidedBy=local, got %q", first.DecidedBy)
	}
	if want := store.OutcomeDecisionID(runID); first.DecisionID != want {
		t.Errorf("expected decisionId %q, got %q", want, first.DecisionID)
	}

	// Second POST with a DIFFERENT verdict must not overwrite the first.
	status, body = postOutcome(t, url, "rejected", "changed my mind")
	if status != http.StatusConflict {
		t.Fatalf("second outcome: expected 409, got %d: %s", status, body)
	}
	var conflict struct {
		Error   string `json:"error"`
		Outcome struct {
			Verdict string `json:"verdict"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Outcome.Verdict != "accepted" {
		t.Errorf("409 must echo the ORIGINAL verdict, got %q", conflict.Outcome.Verdict)
	}

	// The stored decision still carries the first verdict, tagged as an outcome
	// and scoped to the run's project.
	d, err := st.GetRunOutcome(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run outcome: %v", err)
	}
	if d.Decision != "accepted" {
		t.Errorf("stored decision overwritten: %q", d.Decision)
	}
	if d.Project != "hyperplane" {
		t.Errorf("expected project=hyperplane, got %q", d.Project)
	}
	if !hasString(d.Tags, store.OutcomeTag) {
		t.Errorf("expected %q tag, got %v", store.OutcomeTag, d.Tags)
	}
	if !hasString(d.Refs, "run:"+runID) {
		t.Errorf("expected run ref, got %v", d.Refs)
	}
}

// TestRecordOutcomeValidation covers the three refusals: an unknown verdict, an
// unfinished run, and a run that does not exist.
func TestRecordOutcomeValidation(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)

	passed := seedTerminalRun(t, st, "passed")
	if status, body := postOutcome(t, ts.URL+"/api/runs/"+passed+"/outcome", "looks-good", ""); status != http.StatusBadRequest {
		t.Errorf("bad verdict: expected 400, got %d: %s", status, body)
	}

	running := seedTerminalRun(t, st, "running")
	if status, body := postOutcome(t, ts.URL+"/api/runs/"+running+"/outcome", "accepted", ""); status != http.StatusConflict {
		t.Errorf("unfinished run: expected 409, got %d: %s", status, body)
	}

	if status, body := postOutcome(t, ts.URL+"/api/runs/run-does-not-exist/outcome", "accepted", ""); status != http.StatusNotFound {
		t.Errorf("missing run: expected 404, got %d: %s", status, body)
	}
}

// TestRunDetailEmbedsOutcome asserts the Delivery view can learn "decided vs
// not" from the run fetch it already makes — null before, populated after.
func TestRunDetailEmbedsOutcome(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "failed")

	var before struct {
		Outcome *struct {
			Verdict string `json:"verdict"`
		} `json:"outcome"`
	}
	getJSONInto(t, ts.URL+"/api/runs/"+runID, &before)
	if before.Outcome != nil {
		t.Fatalf("expected null outcome before recording, got %+v", before.Outcome)
	}

	if status, body := postOutcome(t, ts.URL+"/api/runs/"+runID+"/outcome", "changes_requested", "needs a real test"); status != http.StatusCreated {
		t.Fatalf("record outcome: %d: %s", status, body)
	}

	var after struct {
		Outcome *struct {
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
		} `json:"outcome"`
	}
	getJSONInto(t, ts.URL+"/api/runs/"+runID, &after)
	if after.Outcome == nil {
		t.Fatal("expected an embedded outcome after recording")
	}
	if after.Outcome.Verdict != "changes_requested" || after.Outcome.Note != "needs a real test" {
		t.Errorf("unexpected embedded outcome: %+v", after.Outcome)
	}
}

// TestInboxSurfacesRunsNeedingOutcome asserts a finished run joins the unified
// human queue until it is decided on, then leaves it.
func TestInboxSurfacesRunsNeedingOutcome(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")

	if !inboxHasOutcome(t, ts.URL, runID) {
		t.Fatal("expected the undecided run in the inbox outcomes queue")
	}

	if status, body := postOutcome(t, ts.URL+"/api/runs/"+runID+"/outcome", "accepted", ""); status != http.StatusCreated {
		t.Fatalf("record outcome: %d: %s", status, body)
	}

	if inboxHasOutcome(t, ts.URL, runID) {
		t.Error("a decided run must drop out of the outcomes queue")
	}
}

// TestInboxSkipsProcessSubRuns asserts a process sub-run never joins the
// "needs decision" queue while its parent does. A sub-run is machinery of the
// parent's delivery — queuing both would ask the human to judge the same
// product twice, the second time under a DAG name they never chose.
func TestInboxSkipsProcessSubRuns(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	parentID := seedTerminalRun(t, st, "passed")
	childID := seedChildRun(t, st, parentID, "pr/data-pipeline", "passed")

	if !inboxHasOutcome(t, ts.URL, parentID) {
		t.Error("the top-level run must appear in the inbox outcomes queue")
	}
	if inboxHasOutcome(t, ts.URL, childID) {
		t.Errorf("sub-run %s must not be queued for its own outcome decision", childID)
	}
}

// subRunSeq keeps seeded sub-run ids unique even when two are created inside
// the same nanosecond tick.
var subRunSeq atomic.Int64

// seedChildRun creates a finished process sub-run beneath parentID. Cleanup is
// registered after the parent's, and t.Cleanup runs last-in-first-out, so the
// child is always deleted first.
func seedChildRun(t *testing.T, st *store.Store, parentID, dagName, state string) string {
	t.Helper()
	runID := fmt.Sprintf("run-sub-%d-%d", time.Now().UnixNano(), subRunSeq.Add(1))
	run := store.Run{RunID: runID, DAG: dagName, State: state, Project: "hyperplane", ParentRun: parentID}
	if err := st.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create sub-run: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = st.DeleteDecision(ctx, store.OutcomeDecisionID(runID))
		_ = st.DeleteRun(ctx, runID)
	})
	return runID
}

// seedNode writes one finished node onto a run.
func seedNode(t *testing.T, st *store.Store, runID, nodeID, kind, state, output string) {
	t.Helper()
	n := store.Node{RunID: runID, NodeID: nodeID, Kind: kind, State: state, Attempt: 1}
	if output != "" {
		n.Output = store.JSON(output)
	}
	if err := st.UpsertNode(context.Background(), n); err != nil {
		t.Fatalf("upsert node %s/%s: %v", runID, nodeID, err)
	}
}

// inboxHasOutcome reports whether GET /api/inbox lists runID as needing a decision.
// inboxHasOutcome reports whether the inbox is asking for a decision on the
// delivery this run belongs to. The queue is journey-grained, so a run appears
// as an ATTEMPT inside one card rather than as a card of its own — which is the
// whole point: a run that a repair or a newer attempt superseded is listed
// there, but the card asks once, about the delivery.
func inboxHasOutcome(t *testing.T, base, runID string) bool {
	t.Helper()
	var inbox struct {
		Outcomes []struct {
			Attempts []struct {
				RunID string `json:"runId"`
			} `json:"attempts"`
		} `json:"outcomes"`
	}
	getJSONInto(t, base+"/api/inbox", &inbox)
	for _, j := range inbox.Outcomes {
		for _, a := range j.Attempts {
			if a.RunID == runID {
				return true
			}
		}
	}
	return false
}

// getJSONInto GETs a URL and decodes the JSON body into dst.
func getJSONInto(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s → %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
