package httpapi_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Saving a workflow out of runs you already did should not start its history at
// zero. Attaching regroups those runs — and only those that really ran the same
// steps with no saved source of their own.
func TestAttachEarlierRunsToSavedWorkflow(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	ctx := t.Context()

	project := "attach-project-" + time.Now().UTC().Format("150405.000000")
	target := t.TempDir()
	definition := store.SDKWorkflow{Name: "Attachable task", Kind: "task", Project: project,
		Instruction: "Write the answer", TargetRoot: target}

	// Two ad-hoc runs of exactly these settings, and one that ran something else.
	adHoc := seedAdHocRun(t, st, project, target, "Write the answer", "passed")
	alsoAdHoc := seedAdHocRun(t, st, project, target, "Write the answer", "failed")
	different := seedAdHocRun(t, st, project, target, "Write something else entirely", "passed")

	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", definition, 201, &saved)

	var history struct {
		Total      int `json:"total"`
		Attachable []struct {
			RunID  string `json:"runId"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"attachable"`
	}
	c.call("GET", "/api/workflows/"+saved.ID+"/runs", nil, 200, &history)
	if history.Total != 0 {
		t.Fatalf("a new workflow has no runs of its own: %d", history.Total)
	}
	offered := map[string]string{}
	for _, item := range history.Attachable {
		offered[item.RunID] = item.Reason
	}
	if len(offered) != 2 || offered[adHoc] == "" || offered[alsoAdHoc] == "" {
		t.Fatalf("attachable offered %v", offered)
	}
	if _, wrong := offered[different]; wrong {
		t.Fatal("a run of different steps must never be offered")
	}
	if offered[adHoc] != store.AttachReason {
		t.Fatalf("reason %q", offered[adHoc])
	}

	var result struct {
		Attached []string `json:"attached"`
		Skipped  []struct {
			RunID  string `json:"runId"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	c.call("POST", "/api/workflows/"+saved.ID+"/attach-runs",
		map[string]any{"runIds": []string{adHoc, alsoAdHoc, different}}, 200, &result)
	if len(result.Attached) != 2 {
		t.Fatalf("attached %v", result.Attached)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].RunID != different {
		t.Fatalf("skipped %+v", result.Skipped)
	}
	if result.Skipped[0].Reason == "" {
		t.Fatal("a refusal must say why")
	}

	// The history now holds them, and the offer is spent.
	c.call("GET", "/api/workflows/"+saved.ID+"/runs", nil, 200, &history)
	if history.Total != 2 {
		t.Fatalf("history total %d, want 2", history.Total)
	}
	if len(history.Attachable) != 0 {
		t.Fatalf("still offering %+v", history.Attachable)
	}

	// Attaching moved identity only: the frozen request is byte-for-byte intact,
	// and the journal says what happened.
	run, err := st.GetRun(ctx, adHoc)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.RunDocString(run, "recipe"); got != "" {
		t.Fatalf("unexpected doc change: %q", got)
	}
	var options struct {
		Request struct {
			Instruction string `json:"instruction"`
		} `json:"request"`
		ProjectWorkflow struct {
			Key              string `json:"key"`
			SourceWorkflowID string `json:"sourceWorkflowId"`
			AttachedFrom     string `json:"attachedFrom"`
			AttachedAt       string `json:"attachedAt"`
		} `json:"projectWorkflow"`
	}
	decodeJSON(t, run.Options, &options)
	if options.Request.Instruction != "Write the answer" {
		t.Fatalf("the executed request must not change: %q", options.Request.Instruction)
	}
	if options.ProjectWorkflow.Key != store.SavedWorkflowKey(saved.ID) ||
		options.ProjectWorkflow.SourceWorkflowID != saved.ID ||
		options.ProjectWorkflow.AttachedAt == "" ||
		options.ProjectWorkflow.AttachedFrom == "" {
		t.Fatalf("identity %+v", options.ProjectWorkflow)
	}
	events, err := st.ListEvents(ctx, adHoc, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "run.attached" {
			found = true
		}
	}
	if !found {
		t.Fatal("attaching a run must leave a journal entry on it")
	}

	// Attaching twice is refused by name, not silently repeated.
	c.call("POST", "/api/workflows/"+saved.ID+"/attach-runs", map[string]any{"runIds": []string{adHoc}}, 200, &result)
	if len(result.Attached) != 0 || len(result.Skipped) != 1 {
		t.Fatalf("re-attach attached=%v skipped=%+v", result.Attached, result.Skipped)
	}
	c.call("POST", "/api/workflows/"+saved.ID+"/attach-runs", map[string]any{"runIds": []string{}}, 400, nil)
	c.call("POST", "/api/workflows/does-not-exist/attach-runs", map[string]any{"runIds": []string{adHoc}}, 404, nil)
}

// seedAdHocRun records a finished SDK run that was launched from an editor
// rather than a saved workflow — the shape an attachable run really has.
func seedAdHocRun(t *testing.T, st *store.Store, project, target, instruction, state string) string {
	t.Helper()
	ctx := t.Context()
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"workflow": "checked-task", "instruction": instruction,
		"workspace": resolved, "autoRepair": false, "model": "gpt-6-astra"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := st.PrepareProjectWorkflow(ctx, raw, "", "", instruction)
	if err != nil {
		t.Fatal(err)
	}
	options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request,
		"projectWorkflow": identity, "workflowVersion": 1, "model": "gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	runID := "20260101-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-checked-task"
	started := time.Now().UTC().Add(-time.Hour)
	run := store.Run{RunID: runID, DAG: "checked-task", Goal: instruction, State: state,
		Mode: "run", Type: "sdk-task", Project: project, StartedAt: started,
		Options: store.JSON(options),
		Doc:     store.JSON(`{"sdk":{"phase":"passed","verification":"passed"}}`)}
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
	return runID
}

// decodeJSON reads a stored jsonb column into a shape a test asserts on.
func decodeJSON(t *testing.T, raw store.JSON, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}
