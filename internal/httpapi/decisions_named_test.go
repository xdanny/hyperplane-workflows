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

// decisionRow is the ledger row both the project page and the Inbox's Decided
// tab render.
type decisionRow struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Decision     string   `json:"decision"`
	Why          string   `json:"why"`
	Source       string   `json:"source"`
	Refs         []string `json:"refs"`
	DecidedBy    string   `json:"decidedBy"`
	RunID        string   `json:"runId"`
	WorkflowName string   `json:"workflowName"`
	RunNumber    int      `json:"runNumber"`
}

// S-13 / D-15: a rejection was filed as "accepted" on both ledgers, because
// they rendered the record's `status` — the ADR lifecycle of the ROW — as the
// verdict. The verdict is `decision`, and the row must also name the work in
// terms a person recognises rather than `journey:j-871c…`.
func TestDecisionLedgerCarriesVerdictAndNamesTheRun(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	project := "ledger-project-" + time.Now().UTC().Format("150405.000000")

	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Weekly topic note", Kind: "nodes", Project: project,
		Instruction: "Write the weekly note",
		Nodes: []sdkworkflow.Node{
			{ID: "draft", Kind: "document", Title: "Write the note", Instruction: "Write a short note."},
		}}, 201, &saved)
	runID := seedSavedWorkflowRun(t, st, saved.ID, saved.Name, project,
		`{"sdk":{"phase":"passed","nodes":[{"id":"draft","phase":"passed","document":{"path":"note.md","bytes":741,"sha256":"aaa"}}]}}`)

	status, body := postBody(t, ts.URL+"/api/runs/"+runID+"/outcome",
		`{"verdict":"rejected","note":"Rejected: the review failed on the word count."}`)
	if status != 201 {
		t.Fatalf("record outcome status=%d body=%s", status, body)
	}

	var rows []decisionRow
	c.call("GET", "/api/decisions?project="+project, nil, 200, &rows)
	if len(rows) != 1 {
		t.Fatalf("expected one decision, got %+v", rows)
	}
	row := rows[0]
	if row.Decision != "rejected" {
		t.Fatalf("the stored verdict must be rejected, got %q (status is %q)", row.Decision, row.Status)
	}
	if row.RunID != runID {
		t.Errorf("row must link to the run it decided: %q want %q", row.RunID, runID)
	}
	if row.WorkflowName != "Weekly topic note" {
		t.Errorf("row must name the workflow, not its key: %q", row.WorkflowName)
	}
	if row.RunNumber != 1 {
		t.Errorf("row must say which run of it: %d", row.RunNumber)
	}
	if row.DecidedBy != store.DecidedByLocal {
		t.Errorf("row must say who decided: %q", row.DecidedBy)
	}
}

// A hand-written decision closes no run. The listing says so by leaving the run
// fields empty rather than inventing a workflow for it.
func TestDecisionLedgerLeavesNonRunDecisionsUnnamed(t *testing.T) {
	_, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	project := "ledger-plain-" + time.Now().UTC().Format("150405.000000")
	status, body := postBody(t, ts.URL+"/api/decisions",
		`{"id":"`+project+`-adr","title":"Use the reviewed SDK workflow","project":"`+project+`","decision":"Use it","source":"owner"}`)
	if status != 201 {
		t.Fatalf("create status=%d body=%s", status, body)
	}
	var rows []decisionRow
	c.call("GET", "/api/decisions?project="+project, nil, 200, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].RunID != "" || rows[0].WorkflowName != "" || rows[0].RunNumber != 0 {
		t.Fatalf("a decision that closed no run must name none: %+v", rows[0])
	}
}

// S-16: a terminal review step records a document too, so a compare view that
// diffed the LAST recorded document diffed "# Check the note" instead of the
// note. The kind is what makes the deliverable identifiable on the client.
func TestWorkflowHistoryDocumentsCarryTheStepKind(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	project := "kind-project-" + time.Now().UTC().Format("150405.000000")

	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Note and review", Kind: "nodes", Project: project,
		Instruction: "Write and review the note",
		Nodes: []sdkworkflow.Node{
			{ID: "draft", Kind: "document", Title: "Write the note", Instruction: "Write a short note."},
			{ID: "check", Kind: "check", Title: "Check the note", Instruction: "Review it."},
		}}, 201, &saved)
	seedNoteAndReviewRun(t, st, saved.ID, saved.Name, project)

	var history struct {
		Runs []struct {
			Documents []struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			} `json:"documents"`
		} `json:"runs"`
	}
	c.call("GET", "/api/workflows/"+saved.ID+"/runs", nil, 200, &history)
	if len(history.Runs) != 1 {
		t.Fatalf("history %+v", history.Runs)
	}
	kinds := map[string]string{}
	for _, doc := range history.Runs[0].Documents {
		kinds[doc.Path] = doc.Kind
	}
	if kinds["review.md"] != "check" {
		t.Fatalf("a review step's document must be marked as one: %v", kinds)
	}
}

// seedNoteAndReviewRun records a run whose review step is the terminal one, so
// its own document is the last in the list.
func seedNoteAndReviewRun(t *testing.T, st *store.Store, workflowID, name, project string) string {
	t.Helper()
	ctx := t.Context()
	request := map[string]any{"workflow": "node-workflow", "instruction": "Write and review the note",
		"nodes": []map[string]any{
			{"id": "draft", "kind": "document", "title": "Write the note", "instruction": "Write a short note."},
			{"id": "check", "kind": "check", "title": "Check the note", "instruction": "Review it."},
		}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := st.PrepareProjectWorkflow(ctx, raw, workflowID, name, "Write and review the note")
	if err != nil {
		t.Fatal(err)
	}
	options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request,
		"projectWorkflow": identity, "workflowVersion": 1, "model": "gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	runID := "20260101-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-node-workflow"
	started := time.Now().UTC().Add(-20 * time.Minute)
	if err := st.CreateRun(ctx, store.Run{RunID: runID, DAG: "node-workflow", Goal: "Write and review the note",
		State: "passed", Mode: "run", Project: project, StartedAt: started, EndedAt: started.Add(time.Minute),
		Options: store.JSON(options), Doc: store.JSON(`{"sdk":{"phase":"passed","nodes":[
			{"id":"draft","phase":"passed","document":{"path":"note.md","bytes":741,"sha256":"aaa"}},
			{"id":"check","phase":"passed","document":{"path":"review.md","bytes":120,"sha256":"bbb"}}]}}`)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
	return runID
}
