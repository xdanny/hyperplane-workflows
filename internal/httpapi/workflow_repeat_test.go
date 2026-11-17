package httpapi_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// The round's whole point, end to end: a workflow saved once with the questions
// it asks, launched twice with different answers, and then readable afterwards
// as two distinguishable runs of ONE workflow rather than two identical rows.
func TestSavedWorkflowRunsAgainWithNewInputs(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/node-progress"))
	t.Setenv("SDK_FIXTURE_CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("SDK_FIXTURE_TURN_SECONDS", "0")
	_, ts := newTestServer(t, nil)

	definition := store.SDKWorkflow{
		Name: "Weekly competitor brief", Kind: "nodes", Project: "weekly-brief",
		Instruction: "Write the competitor brief for {slot:week}.",
		Inputs: []store.ContextSlot{
			{ID: "week", Label: "Week", Hint: "The Monday-Sunday week the brief covers.", Required: true, Example: "2026-09-07"},
		},
		Nodes: []sdkworkflow.Node{
			{ID: "write", Title: "Write the brief", Instruction: "Summarise the week of {slot:week}."},
			{ID: "review", Kind: "check", Title: "Independent review", Instruction: "Check the brief names its sources."},
		},
	}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/workflows", definition)
	var saved store.SDKWorkflow
	if status != 201 || json.Unmarshal(raw, &saved) != nil {
		t.Fatalf("save: %d %s", status, raw)
	}
	if len(saved.Inputs) != 1 || !strings.Contains(saved.Instruction, "{slot:week}") {
		t.Fatalf("saving did not keep the workflow reusable: %+v", saved)
	}

	// A required input left out names itself rather than failing obscurely.
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"sourceWorkflowId": saved.ID})
	var rejected struct {
		MissingSlots []string `json:"missingSlots"`
	}
	if status != 400 || json.Unmarshal(raw, &rejected) != nil || len(rejected.MissingSlots) != 1 || rejected.MissingSlots[0] != "week" {
		t.Fatalf("a missing required input was not named: %d %s", status, raw)
	}

	// A stale revision is refused with the current one, not run silently.
	stale := saved.Revision + 7
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"sourceWorkflowId": saved.ID, "revision": stale, "slots": map[string]string{"week": "2026-09-07"}})
	var conflict struct {
		Revision int64 `json:"revision"`
	}
	if status != 409 || json.Unmarshal(raw, &conflict) != nil || conflict.Revision != saved.Revision {
		t.Fatalf("a stale revision was not refused with the current one: %d %s", status, raw)
	}

	weeks := []string{"2026-08-31", "2026-09-07"}
	runIDs := make([]string, 0, len(weeks))
	for _, week := range weeks {
		status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{
			"sourceWorkflowId": saved.ID, "revision": saved.Revision, "slots": map[string]string{"week": week}})
		var launch struct{ RunID string }
		if status != 202 || json.Unmarshal(raw, &launch) != nil {
			t.Fatalf("launch %s: %d %s", week, status, raw)
		}
		runIDs = append(runIDs, launch.RunID)
		waitForTerminalRun(t, ts.URL, launch.RunID)
	}

	// The launch record says which values produced this run, and which saved
	// revision it was read from.
	_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+runIDs[1], nil)
	var detail struct {
		Run struct {
			store.Run
			WorkflowName     string            `json:"workflowName"`
			RunNumber        int               `json:"runNumber"`
			DurationMs       *int64            `json:"durationMs"`
			Slots            map[string]string `json:"slots"`
			WaitingForPerson bool              `json:"waitingForPerson"`
			FailingStep      *struct{ ID, Title, Kind string }
		} `json:"run"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Run.WorkflowName != "Weekly competitor brief" || detail.Run.RunNumber != 2 {
		t.Fatalf("a run of a saved workflow is not identified as one: %+v", detail.Run)
	}
	if detail.Run.Slots["week"] != "2026-09-07" || detail.Run.DurationMs == nil || detail.Run.FailingStep != nil {
		t.Fatalf("run detail lost this run's values or duration: %+v", detail.Run)
	}
	var launchRecord struct {
		Request         struct{ Slots map[string]string }
		ProjectWorkflow struct {
			Key            string
			SourceRevision int64
		}
	}
	if err := json.Unmarshal(detail.Run.Options, &launchRecord); err != nil {
		t.Fatal(err)
	}
	if launchRecord.Request.Slots["week"] != "2026-09-07" {
		t.Fatalf("the launch record did not keep the accepted values: %+v", launchRecord)
	}
	if launchRecord.ProjectWorkflow.Key != "saved:"+saved.ID || launchRecord.ProjectWorkflow.SourceRevision != saved.Revision {
		t.Fatalf("lineage lost the saved source: %+v", launchRecord.ProjectWorkflow)
	}
	// The filled instruction ran; the saved definition still asks the question.
	if !strings.Contains(detail.Run.Goal, "2026-09-07") {
		t.Fatalf("the run did not use this run's values: %q", detail.Run.Goal)
	}

	// Both runs are one workflow's series in the list, numbered and separable.
	_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs?project=weekly-brief", nil)
	var list []struct {
		RunID        string            `json:"runId"`
		WorkflowName string            `json:"workflowName"`
		RunNumber    int               `json:"runNumber"`
		Slots        map[string]string `json:"slots"`
		DurationMs   *int64            `json:"durationMs"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	numbers := map[string]int{}
	for _, row := range list {
		numbers[row.RunID] = row.RunNumber
		if row.WorkflowName != "Weekly competitor brief" || row.DurationMs == nil {
			t.Fatalf("a list row does not name its workflow or its duration: %+v", row)
		}
		if row.Slots["week"] == "" {
			t.Fatalf("a list row does not say which values it ran with: %+v", row)
		}
	}
	if numbers[runIDs[0]] != 1 || numbers[runIDs[1]] != 2 {
		t.Fatalf("runs of one workflow are not numbered in order: %+v", numbers)
	}

	// The workflow's own history answers "what has this produced, and when".
	_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/workflows/"+saved.ID+"/runs?limit=50", nil)
	var history struct {
		Workflow store.SDKWorkflow `json:"workflow"`
		Total    int               `json:"total"`
		Runs     []struct {
			RunID          string            `json:"runId"`
			RunNumber      int               `json:"runNumber"`
			DurationMs     *int64            `json:"durationMs"`
			State          string            `json:"state"`
			Slots          map[string]string `json:"slots"`
			ChecksDeclared int               `json:"checksDeclared"`
			ChecksPassed   int               `json:"checksPassed"`
			Outcome        *struct{ Verdict string }
			Usage          *struct{ InputTokens int }
			Documents      []struct {
				StepID string `json:"stepId"`
				Title  string `json:"title"`
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"documents"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &history); err != nil {
		t.Fatal(err)
	}
	if history.Total != 2 || len(history.Runs) != 2 || len(history.Workflow.Inputs) != 1 {
		t.Fatalf("workflow history did not list both runs: %+v", history)
	}
	if history.Runs[0].RunNumber != 2 || history.Runs[0].Slots["week"] != "2026-09-07" {
		t.Fatalf("history is not newest-first with its values: %+v", history.Runs[0])
	}
	if history.Runs[0].ChecksDeclared != 1 || history.Runs[0].ChecksPassed != 1 {
		t.Fatalf("history lost the independent review result: %+v", history.Runs[0])
	}
	if history.Runs[0].Usage == nil || history.Runs[0].Usage.InputTokens == 0 {
		t.Fatalf("history lost the recorded usage: %+v", history.Runs[0])
	}
	// Documents are the results — the step nothing waits on, not every note.
	if len(history.Runs[0].Documents) != 1 || history.Runs[0].Documents[0].StepID != "review" {
		t.Fatalf("history did not report the produced result: %+v", history.Runs[0].Documents)
	}
	if history.Runs[0].Documents[0].SHA256 == "" || history.Runs[0].Documents[0].Title != "Independent review" {
		t.Fatalf("a produced document lost its identity: %+v", history.Runs[0].Documents[0])
	}
}

// A failed independent review names the STEP a person wrote, never the internal
// delivery node, and the failure is readable without opening the run.
func TestFailingStepNamesTheDeclaredStep(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/node-progress"))
	t.Setenv("SDK_FIXTURE_CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("SDK_FIXTURE_TURN_SECONDS", "0")
	t.Setenv("SDK_FIXTURE_REVIEW", "fail")
	_, ts := newTestServer(t, nil)

	nodes := []sdkworkflow.Node{
		{ID: "write", Title: "Write the brief", Instruction: "Summarise the week."},
		{ID: "review", Kind: "check", Title: "Independent review", Instruction: "Check the brief names its sources."},
	}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": "Write the brief", "nodes": nodes})
	var launch struct{ RunID string }
	if status != 202 || json.Unmarshal(raw, &launch) != nil {
		t.Fatalf("launch: %d %s", status, raw)
	}
	waitForTerminalRun(t, ts.URL, launch.RunID)

	_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
	var detail struct {
		Run struct {
			State       string `json:"state"`
			FailingStep *struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Kind  string `json:"kind"`
			} `json:"failingStep"`
		} `json:"run"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Run.State != "failed" {
		t.Fatalf("a rejected review did not fail the run: %+v", detail.Run)
	}
	if detail.Run.FailingStep == nil || detail.Run.FailingStep.ID != "review" ||
		detail.Run.FailingStep.Title != "Independent review" || detail.Run.FailingStep.Kind != "check" {
		t.Fatalf("the failing step is not the step a person wrote: %+v", detail.Run.FailingStep)
	}
}

// waitForTerminalRun polls until the run stops, the way a reader does.
func waitForTerminalRun(t *testing.T, base, runID string) store.Run {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, raw := doJSON(t, http.MethodGet, base+"/api/runs/"+runID, nil)
		var detail struct{ Run store.Run }
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Run.State != "running" {
			return detail.Run
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s never finished", runID)
	return store.Run{}
}

// An inbox card that names nothing but a hash asks a person to decide something
// they cannot see. This requires the workflow's name, which run it is, what the
// run is for, what happens once they approve, and the document to read.
func TestInboxCardsCarryTheirRunContext(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/node-progress"))
	t.Setenv("SDK_FIXTURE_CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("SDK_FIXTURE_TURN_SECONDS", "0")
	_, ts := newTestServer(t, nil)

	definition := store.SDKWorkflow{
		Name: "Monthly supplier review", Kind: "nodes", Project: "supplier-review",
		Instruction: "Review suppliers for {slot:month}.",
		Inputs:      []store.ContextSlot{{ID: "month", Label: "Month", Required: true}},
		Nodes: []sdkworkflow.Node{
			{ID: "draft", Title: "Draft the review", Instruction: "Draft the review for {slot:month}."},
			{ID: "signoff", Kind: "approval", Title: "Approve the draft", Instruction: "Read the draft before it is published."},
			{ID: "publish", Title: "Write the final note", Instruction: "Write the approved note."},
		},
	}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/workflows", definition)
	var saved store.SDKWorkflow
	if status != 201 || json.Unmarshal(raw, &saved) != nil {
		t.Fatalf("save: %d %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{
		"sourceWorkflowId": saved.ID, "slots": map[string]string{"month": "September 2026"}})
	var launch struct{ RunID string }
	if status != 202 || json.Unmarshal(raw, &launch) != nil {
		t.Fatalf("launch: %d %s", status, raw)
	}

	var approvalID string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && approvalID == "" {
		_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/inbox", nil)
		var inbox struct {
			Approvals []struct {
				ID            string   `json:"id"`
				RunID         string   `json:"runId"`
				WorkflowName  string   `json:"workflowName"`
				RunNumber     int      `json:"runNumber"`
				Goal          string   `json:"goal"`
				NextSteps     []string `json:"nextSteps"`
				DraftDocument *struct {
					Path string `json:"path"`
					URL  string `json:"url"`
				} `json:"draftDocument"`
			} `json:"approvals"`
		}
		if err := json.Unmarshal(raw, &inbox); err != nil {
			t.Fatal(err)
		}
		for _, item := range inbox.Approvals {
			if item.RunID != launch.RunID {
				continue
			}
			if item.WorkflowName != "Monthly supplier review" || item.RunNumber != 1 {
				t.Fatalf("the approval card does not say which workflow or run it is: %+v", item)
			}
			if !strings.Contains(item.Goal, "September 2026") {
				t.Fatalf("the approval card does not say what the run is for: %+v", item)
			}
			if len(item.NextSteps) != 1 || item.NextSteps[0] != "Write the final note" {
				t.Fatalf("the approval card does not say what happens next: %+v", item.NextSteps)
			}
			if item.DraftDocument == nil || item.DraftDocument.Path != "draft.md" || item.DraftDocument.URL == "" {
				t.Fatalf("the approval card does not link the document to read: %+v", item.DraftDocument)
			}
			approvalID = item.ID
		}
		if approvalID == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if approvalID == "" {
		t.Fatal("the approval never reached the inbox")
	}

	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/approvals/"+approvalID+"/decision", map[string]string{"decision": "approved"})
	if status != 200 {
		t.Fatalf("decide: %d %s", status, raw)
	}
	if run := waitForTerminalRun(t, ts.URL, launch.RunID); run.State != "passed" {
		t.Fatalf("run did not pass after approval: %s %s", run.State, run.Doc)
	}

	// The delivery awaiting a verdict carries the same context, plus the result.
	_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/inbox", nil)
	var inbox struct {
		Outcomes []struct {
			WorkflowName   string `json:"workflowName"`
			RunNumber      int    `json:"runNumber"`
			ResultDocument *struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"resultDocument"`
			Attempts []struct {
				RunID string `json:"runId"`
			} `json:"attempts"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &inbox); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range inbox.Outcomes {
		for _, attempt := range item.Attempts {
			if attempt.RunID != launch.RunID {
				continue
			}
			found = true
			if item.WorkflowName != "Monthly supplier review" || item.RunNumber != 1 {
				t.Fatalf("the sign-off card does not say which workflow or run it is: %+v", item)
			}
			if item.ResultDocument == nil || item.ResultDocument.Title != "Write the final note" || item.ResultDocument.URL == "" {
				t.Fatalf("the sign-off card does not link what was produced: %+v", item.ResultDocument)
			}
		}
	}
	if !found {
		t.Fatalf("the finished delivery never asked for a verdict: %s", raw)
	}
}
