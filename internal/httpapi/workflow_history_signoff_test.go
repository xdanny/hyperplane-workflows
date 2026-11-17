package httpapi_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A history table that read one CURRENT sign-off policy over every row restated
// old runs under terms they never ran with. Each row carries the policy frozen
// in its own launch record, and a run that recorded none is "required" —
// which is what those runs actually did.
func TestWorkflowHistoryRowsCarryTheirOwnSignOffPolicy(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	project := "signoff-history-" + time.Now().UTC().Format("150405.000000")

	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Sign-off history", Kind: "nodes", Project: project,
		Instruction: "Write the weekly brief",
		Nodes: []sdkworkflow.Node{
			{ID: "draft", Kind: "document", Title: "Draft the brief", Instruction: "Write a short brief."},
		}}, 201, &saved)

	// Two runs of the same saved workflow, launched under different policies.
	optional := seedSignOffRun(t, st, saved.ID, saved.Name, project, store.SignOffOptional)
	legacy := seedSignOffRun(t, st, saved.ID, saved.Name, project, "")

	var history struct {
		Runs []struct {
			RunID   string `json:"runId"`
			SignOff string `json:"signOff"`
		} `json:"runs"`
	}
	c.call("GET", "/api/workflows/"+saved.ID+"/runs", nil, 200, &history)
	policies := map[string]string{}
	for _, run := range history.Runs {
		policies[run.RunID] = run.SignOff
	}
	if policies[optional] != store.SignOffOptional {
		t.Errorf("run launched as optional reads %q", policies[optional])
	}
	if policies[legacy] != store.SignOffRequired {
		t.Errorf("run with no recorded policy reads %q, want %q", policies[legacy], store.SignOffRequired)
	}
}

// seedSignOffRun records a finished run of a saved workflow whose frozen launch
// record carries policy (or none at all, for a record that predates the field).
func seedSignOffRun(t *testing.T, st *store.Store, workflowID, name, project, policy string) string {
	t.Helper()
	ctx := t.Context()
	request := map[string]any{"workflow": "node-workflow", "instruction": "Write the weekly brief",
		"nodes": []map[string]any{{"id": "draft", "kind": "document", "title": "Draft the brief", "instruction": "Write a short brief."}}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := st.PrepareProjectWorkflow(ctx, raw, workflowID, name, "Write the weekly brief")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(identity, &fields); err != nil {
		t.Fatal(err)
	}
	if policy != "" {
		fields["signOff"] = policy
	}
	options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request,
		"projectWorkflow": fields, "workflowVersion": 1, "model": "gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	runID := "20260101-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-node-workflow"
	started := time.Now().UTC().Add(-30 * time.Minute)
	if err := st.CreateRun(ctx, store.Run{RunID: runID, DAG: "node-workflow", Goal: "Write the weekly brief",
		State: "passed", Mode: "run", Project: project, StartedAt: started, EndedAt: started.Add(time.Minute),
		Options: store.JSON(options), Doc: store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"draft","phase":"passed"}]}}`)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
	return runID
}
