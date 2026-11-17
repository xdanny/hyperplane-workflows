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

// Comparing two runs on their prose alone hid a changed data file. History rows
// carry every file each step recorded, with the checksum that makes "same" or
// "changed" a fact rather than an impression.
func TestWorkflowHistoryCarriesEveryRecordedFile(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	project := "files-project-" + time.Now().UTC().Format("150405.000000")

	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Two step brief", Kind: "nodes", Project: project,
		Instruction: "Write the weekly brief",
		Nodes: []sdkworkflow.Node{
			{ID: "draft", Kind: "document", Title: "Draft the brief", Instruction: "Write a short brief."},
			{ID: "totals", Kind: "task", Title: "Compute totals", Instruction: "Write build/totals.csv."},
		}}, 201, &saved)

	runID := seedSavedWorkflowRun(t, st, saved.ID, saved.Name, project, `{"sdk":{"phase":"passed","nodes":[
		{"id":"draft","phase":"passed","document":{"path":"draft.md","bytes":120,"sha256":"aaa"}},
		{"id":"totals","phase":"passed","document":{"path":"totals.md","bytes":40,"sha256":"bbb"},
		 "files":[{"path":"build/totals.csv","bytes":900,"sha256":"ccc"},{"path":"totals.md","bytes":40,"sha256":"bbb"}]}]}}`)

	var history struct {
		Runs []struct {
			RunID string `json:"runId"`
			Files []struct {
				StepID    string `json:"stepId"`
				StepTitle string `json:"stepTitle"`
				Path      string `json:"path"`
				Bytes     int64  `json:"bytes"`
				SHA256    string `json:"sha256"`
			} `json:"files"`
			Documents []struct {
				Path string `json:"path"`
			} `json:"documents"`
		} `json:"runs"`
	}
	c.call("GET", "/api/workflows/"+saved.ID+"/runs", nil, 200, &history)
	if len(history.Runs) != 1 || history.Runs[0].RunID != runID {
		t.Fatalf("history: %+v", history.Runs)
	}
	files := map[string]string{}
	titles := map[string]string{}
	for _, f := range history.Runs[0].Files {
		files[f.Path] = f.SHA256
		titles[f.Path] = f.StepTitle
	}
	for path, want := range map[string]string{"draft.md": "aaa", "totals.md": "bbb", "build/totals.csv": "ccc"} {
		if files[path] != want {
			t.Errorf("file %s checksum %q, want %q (all: %v)", path, files[path], want, files)
		}
	}
	if len(history.Runs[0].Files) != 3 {
		t.Errorf("a file recorded twice by one step must be listed once: %+v", history.Runs[0].Files)
	}
	if titles["build/totals.csv"] != "Compute totals" {
		t.Errorf("a file must name the step that produced it: %q", titles["build/totals.csv"])
	}
	// The documents a run DELIVERED remain a separate, narrower list.
	if len(history.Runs[0].Documents) != 1 || history.Runs[0].Documents[0].Path != "totals.md" {
		t.Errorf("delivered documents: %+v", history.Runs[0].Documents)
	}
}

// The runs list shows when a run started, so it is ordered by that. Ordering by
// last update put a run that started at 12:50 between 13:16 and 12:57.
func TestRunsListIsOrderedByStart(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	ctx := t.Context()
	project := "order-project-" + time.Now().UTC().Format("150405.000000")

	base := time.Now().UTC().Add(-4 * time.Hour)
	// The run that started FIRST was touched LAST — the case that used to scramble.
	early := seedRunAt(t, st, project, base, base.Add(3*time.Hour))
	middle := seedRunAt(t, st, project, base.Add(time.Hour), base.Add(time.Hour))
	late := seedRunAt(t, st, project, base.Add(2*time.Hour), base.Add(2*time.Hour))
	_ = ctx

	var runs []struct {
		RunID     string    `json:"runId"`
		StartedAt time.Time `json:"startedAt"`
	}
	c.call("GET", "/api/runs?project="+project, nil, 200, &runs)
	got := []string{}
	for _, run := range runs {
		got = append(got, run.RunID)
	}
	want := []string{late, middle, early}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// seedSavedWorkflowRun records a finished run of a saved workflow, carrying the
// committed checkpoint a test needs to read back.
func seedSavedWorkflowRun(t *testing.T, st *store.Store, workflowID, name, project, doc string) string {
	t.Helper()
	ctx := t.Context()
	request := map[string]any{"workflow": "node-workflow", "instruction": "Write the weekly brief",
		"nodes": []map[string]any{
			{"id": "draft", "kind": "document", "title": "Draft the brief", "instruction": "Write a short brief."},
			{"id": "totals", "kind": "task", "title": "Compute totals", "instruction": "Write build/totals.csv."},
		}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := st.PrepareProjectWorkflow(ctx, raw, workflowID, name, "Write the weekly brief")
	if err != nil {
		t.Fatal(err)
	}
	options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request,
		"projectWorkflow": identity, "workflowVersion": 1, "model": "gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	runID := "20260101-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-node-workflow"
	started := time.Now().UTC().Add(-30 * time.Minute)
	if err := st.CreateRun(ctx, store.Run{RunID: runID, DAG: "node-workflow", Goal: "Write the weekly brief",
		State: "passed", Mode: "run", Project: project, StartedAt: started, EndedAt: started.Add(time.Minute),
		Options: store.JSON(options), Doc: store.JSON(doc)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
	return runID
}

// seedRunAt records a finished run with an explicit start and last-update time.
func seedRunAt(t *testing.T, st *store.Store, project string, started, updated time.Time) string {
	t.Helper()
	ctx := t.Context()
	runID := "20260101-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-node-workflow"
	if err := st.CreateRun(ctx, store.Run{RunID: runID, DAG: "node-workflow", Goal: "A run", State: "passed",
		Mode: "run", Project: project, StartedAt: started, EndedAt: updated,
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"A run"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET updated_at=$2 WHERE run_id=$1`, runID, updated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
	return runID
}
