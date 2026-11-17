package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A saved workflow says whether a person must sign off on what it delivers.
// These tests hold the three policies to their promises: "required" keeps
// today's behavior, "optional" finishes without queuing anybody, and
// "auto_when_checks_pass" accepts a run ONLY when it declared checks and passed
// all of them — and says, on the record, that no person decided it.

type signOffClient struct {
	t   *testing.T
	url string
}

func (c signOffClient) call(method, path string, body any, status int, out any) {
	c.t.Helper()
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(method, c.url+path, bytes.NewReader(data))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		var detail any
		_ = json.NewDecoder(res.Body).Decode(&detail)
		c.t.Fatalf("%s %s: status %d (want %d): %v", method, path, res.StatusCode, status, detail)
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			c.t.Fatal(err)
		}
	}
}

func TestSignOffPolicyIsStoredAndValidated(t *testing.T) {
	_, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}

	// The default is what every workflow did before the setting existed.
	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Default policy", Kind: "task",
		Instruction: "Write the answer", TargetRoot: t.TempDir()}, 201, &saved)
	if saved.SignOff != store.SignOffRequired {
		t.Fatalf("default sign-off: %q", saved.SignOff)
	}

	saved.SignOff = store.SignOffOptional
	c.call("PUT", "/api/workflows/"+saved.ID, saved, 200, &saved)
	if saved.SignOff != store.SignOffOptional {
		t.Fatalf("stored sign-off: %q", saved.SignOff)
	}
	var reread []store.SDKWorkflow
	c.call("GET", "/api/workflows", nil, 200, &reread)
	found := false
	for _, item := range reread {
		if item.ID == saved.ID {
			found = item.SignOff == store.SignOffOptional
		}
	}
	if !found {
		t.Fatal("stored sign-off policy missing from the workflow list")
	}

	// An unknown policy is a client mistake, not a behavior to guess at.
	unknown := saved
	unknown.SignOff = "whenever_someone_feels_like_it"
	c.call("PUT", "/api/workflows/"+saved.ID, unknown, 400, nil)
}

// signOffRun saves a workflow under one policy, launches it from its saved
// source and returns the finished run id.
func signOffRun(t *testing.T, c signOffClient, policy string, check []string) string {
	t.Helper()
	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Sign-off " + policy, Kind: "task",
		Instruction: "Write the answer", TargetRoot: t.TempDir(), Check: check, SignOff: policy}, 201, &saved)

	var launch struct {
		RunID string `json:"runId"`
	}
	c.call("POST", "/api/run", map[string]any{"sourceWorkflowId": saved.ID}, 202, &launch)
	var detail struct {
		Run struct {
			State   string `json:"state"`
			SignOff string `json:"signOff"`
		} `json:"run"`
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		c.call("GET", "/api/runs/"+launch.RunID, nil, 200, &detail)
		if detail.Run.State != "running" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if detail.Run.State != "passed" {
		t.Fatalf("run state %q", detail.Run.State)
	}
	// The policy is captured on the run, not read back off the workflow.
	if detail.Run.SignOff != policy {
		t.Fatalf("run sign-off %q, want %q", detail.Run.SignOff, policy)
	}
	return launch.RunID
}

// journeyForRun finds the delivery a run belongs to.
func journeyForRun(t *testing.T, c signOffClient, runID string) journey.Journey {
	t.Helper()
	var journeys []journey.Journey
	c.call("GET", "/api/journeys", nil, 200, &journeys)
	for _, j := range journeys {
		for _, a := range j.Attempts {
			if a.RunID == runID {
				return j
			}
		}
	}
	t.Fatalf("no delivery lists run %s", runID)
	return journey.Journey{}
}

// queuedInInbox reports whether the inbox is still asking a person about this run.
func queuedInInbox(t *testing.T, c signOffClient, runID string) bool {
	t.Helper()
	var inbox struct {
		Outcomes []journey.Journey `json:"outcomes"`
	}
	c.call("GET", "/api/inbox", nil, 200, &inbox)
	for _, j := range inbox.Outcomes {
		for _, a := range j.Attempts {
			if a.RunID == runID {
				return true
			}
		}
	}
	return false
}

func TestSignOffRequiredStillQueues(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "codex", "testdata", "task"))
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	_, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	runID := signOffRun(t, c, store.SignOffRequired,
		[]string{"python3", "-c", "from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42'"})

	j := journeyForRun(t, c, runID)
	if j.Stage != journey.StageNeedsDecision || !j.NeedsDecision {
		t.Fatalf("stage %q needsDecision %v", j.Stage, j.NeedsDecision)
	}
	if j.SignOff != store.SignOffRequired {
		t.Fatalf("journey sign-off %q", j.SignOff)
	}
	if !queuedInInbox(t, c, runID) {
		t.Fatal("a run whose workflow requires sign-off must stay in the inbox")
	}
}

func TestSignOffOptionalFinishesWithoutQueuing(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "codex", "testdata", "task"))
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	_, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	runID := signOffRun(t, c, store.SignOffOptional, nil)

	j := journeyForRun(t, c, runID)
	if j.Stage != journey.StageDone {
		t.Fatalf("stage %q, want %q", j.Stage, journey.StageDone)
	}
	if j.NeedsDecision {
		t.Fatal("an optional sign-off must not ask for a decision")
	}
	if j.SignOff != store.SignOffOptional {
		t.Fatalf("journey sign-off %q", j.SignOff)
	}
	if queuedInInbox(t, c, runID) {
		t.Fatal("an optional sign-off must not queue in the inbox")
	}
	// Still decidable: not queuing is not the same as not allowing.
	c.call("POST", "/api/journeys/"+j.Key+"/outcome",
		map[string]any{"runId": runID, "verdict": "accepted", "note": "read it, happy with it"}, 201, nil)
	decided := journeyForRun(t, c, runID)
	if decided.Stage != journey.StageAccepted {
		t.Fatalf("stage after deciding: %q", decided.Stage)
	}
	if decided.Outcome == nil || decided.Outcome.DecidedBy != store.DecidedByLocal {
		t.Fatalf("a person's decision must be recorded as their own: %+v", decided.Outcome)
	}
}

func TestSignOffAutomaticOnlyWhenDeclaredChecksPass(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal", "codex", "testdata", "task"))
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	_, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}

	runID := signOffRun(t, c, store.SignOffAutoOnChecks,
		[]string{"python3", "-c", "from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42'"})
	var detail struct {
		Outcome *struct {
			Verdict   string `json:"verdict"`
			Note      string `json:"note"`
			DecidedBy string `json:"decidedBy"`
		} `json:"outcome"`
	}
	// The acceptance is written just after the terminal checkpoint commits.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.call("GET", "/api/runs/"+runID, nil, 200, &detail)
		if detail.Outcome != nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if detail.Outcome == nil {
		t.Fatal("a passed run with all declared checks passed was not accepted automatically")
	}
	if detail.Outcome.Verdict != "accepted" || detail.Outcome.DecidedBy != store.DecidedByAuto {
		t.Fatalf("outcome %+v", detail.Outcome)
	}
	if want := "Accepted automatically: all 1 declared checks passed (workflow setting)"; detail.Outcome.Note != want {
		t.Fatalf("note %q, want %q", detail.Outcome.Note, want)
	}
	if queuedInInbox(t, c, runID) {
		t.Fatal("an automatically accepted run must not queue")
	}
	j := journeyForRun(t, c, runID)
	if j.Stage != journey.StageAccepted {
		t.Fatalf("stage %q", j.Stage)
	}
	if j.Outcome == nil || j.Outcome.DecidedBy != store.DecidedByAuto {
		t.Fatalf("the delivery must say who accepted it: %+v", j.Outcome)
	}

	// Zero declared checks is not evidence. That run still asks a person.
	unchecked := signOffRun(t, c, store.SignOffAutoOnChecks, nil)
	var plain struct {
		Outcome *struct {
			Verdict string `json:"verdict"`
		} `json:"outcome"`
	}
	c.call("GET", "/api/runs/"+unchecked, nil, 200, &plain)
	if plain.Outcome != nil {
		t.Fatalf("a run with no declared checks was accepted automatically: %+v", plain.Outcome)
	}
	if !queuedInInbox(t, c, unchecked) {
		t.Fatal("a run with no declared checks must still queue for a person")
	}
	if j := journeyForRun(t, c, unchecked); j.Stage != journey.StageNeedsDecision {
		t.Fatalf("stage %q, want %q", j.Stage, journey.StageNeedsDecision)
	}
}
