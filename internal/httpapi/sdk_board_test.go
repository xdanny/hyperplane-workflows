package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func seedSDKBoard(t *testing.T, st *store.Store, target string) (*store.Lane, []store.Ticket) {
	t.Helper()
	id := fmt.Sprintf("board-%d", time.Now().UnixNano())
	lane := &store.Lane{ID: id, Name: "SDK Board", Status: "active", TargetRoot: target, Project: id}
	tasks := []store.Ticket{{ID: id + "-one", LaneID: id, Title: "Write answer", Detail: "Create answer.txt. Human review follows.", Status: "todo", Priority: 3}, {ID: id + "-two", LaneID: id, Title: "Follow up", Detail: "Update answer.txt after review.", Status: "todo", Priority: 2, Deps: []string{id + "-one"}}}
	if err := st.CreateLaneWithTickets(t.Context(), lane, tasks); err != nil {
		t.Fatal(err)
	}
	return lane, tasks
}
func setupSDKBoard(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/task"))
	t.Setenv("SDK_FIXTURE_MODE", mode)
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SDK_FIXTURE_CALLS", calls)
	return calls
}
func waitSDKBoardRun(t *testing.T, st *store.Store, id string) store.Run {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != "running" {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return store.Run{}
}
func boardLaunch(t *testing.T, base, lane string) (int, string, []byte) {
	t.Helper()
	status, raw := doJSON(t, "POST", base+"/api/lanes/"+lane+"/work", map[string]int{"count": 1})
	var body struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal(raw, &body)
	return status, body.RunID, raw
}

func TestSDKBoardTaskLifecycleAndHumanDependencyGate(t *testing.T) {
	calls := setupSDKBoard(t, "ordinary")
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	lane, tasks := seedSDKBoard(t, st, t.TempDir())
	status, id, raw := boardLaunch(t, ts.URL, lane.ID)
	if status != 202 || id == "" {
		t.Fatalf("%d %s", status, raw)
	}
	run := waitSDKBoardRun(t, st, id)
	if run.State != "passed" || !strings.Contains(string(run.Doc), `"not_requested"`) {
		t.Fatalf("run: %+v", run)
	}
	first, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil || first.Status != "review" || first.SDKRunID != id || first.Output != "Updated answer.txt" {
		t.Fatalf("ticket: %+v %v", first, err)
	}
	if status, _, raw := boardLaunch(t, ts.URL, lane.ID); status != 409 {
		t.Fatalf("dependent started before human review: %d %s", status, raw)
	}
	// Wait for the actual owner to release before a human changes the status.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, raw = doJSON(t, "PATCH", ts.URL+"/api/tickets/"+first.ID, map[string]string{"status": "done", "expectedRunId": first.SDKRunID, "expectedUpdatedAt": first.UpdatedAt.Format(time.RFC3339Nano)})
		if status == 200 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != 200 {
		t.Fatalf("human review: %d %s", status, raw)
	}
	status, secondID, raw := boardLaunch(t, ts.URL, lane.ID)
	if status != 202 {
		t.Fatalf("dependent admission: %d %s", status, raw)
	}
	if run := waitSDKBoardRun(t, st, secondID); run.State != "passed" {
		t.Fatalf("dependent run: %+v", run)
	}
	second, err := st.GetTicket(t.Context(), tasks[1].ID)
	if err != nil || second.Status != "review" || second.SDKRunID != secondID {
		t.Fatalf("dependent: %+v %v", second, err)
	}
	data, err := os.ReadFile(calls)
	if err != nil || strings.Count(string(data), "start\n") != 2 || strings.Contains(string(data), "resume") {
		t.Fatalf("unexpected model calls %s %v", data, err)
	}
}

func TestSDKBoardTaskCancelAndDuplicateAdmission(t *testing.T) {
	calls := setupSDKBoard(t, "hang")
	srv, ts := newTestServer(t, nil)
	st := openStore(t)
	lane, tasks := seedSDKBoard(t, st, t.TempDir())
	var wg sync.WaitGroup
	wg.Add(2)
	statuses := make(chan int, 2)
	ids := make(chan string, 2)
	for range 2 {
		go func() {
			defer wg.Done()
			status, id, _ := boardLaunch(t, ts.URL, lane.ID)
			statuses <- status
			if id != "" {
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(statuses)
	close(ids)
	accepted := 0
	for status := range statuses {
		if status == 202 {
			accepted++
		} else if status != 409 {
			t.Errorf("unexpected refusal %d", status)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d", accepted)
	}
	id := <-ids
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(calls); len(data) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	active, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	status, raw := doJSON(t, "PATCH", ts.URL+"/api/tickets/"+tasks[0].ID, map[string]string{"status": "done", "expectedRunId": active.SDKRunID, "expectedUpdatedAt": active.UpdatedAt.Format(time.RFC3339Nano)})
	if status != 409 {
		t.Fatalf("active task mutable: %d %s", status, raw)
	}
	status, raw = doJSON(t, "POST", ts.URL+"/api/runs/"+id+"/cancel", nil)
	if status != 200 {
		t.Fatalf("cancel %d %s", status, raw)
	}
	run := waitSDKBoardRun(t, st, id)
	if run.State != "interrupted" {
		t.Fatalf("state %s", run.State)
	}
	ticket, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil || ticket.Status != "blocked" || ticket.SDKRunID != id {
		t.Fatalf("cancellation not atomic: %+v %v", ticket, err)
	}
	if !srv.DrainRuns(10 * time.Second) {
		t.Fatal("owned task did not drain")
	}
	data, _ := os.ReadFile(calls)
	if strings.Count(string(data), "start\n") != 1 {
		t.Fatalf("duplicate call: %s", data)
	}
	second, _ := st.GetTicket(t.Context(), tasks[1].ID)
	if second.Status != "todo" {
		t.Fatal("cancellation replayed dependent")
	}
}

func TestSDKBoardRefusesUnsafeTargetsAndLegacyFlagsBeforeClaim(t *testing.T) {
	calls := setupSDKBoard(t, "ordinary")
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	for _, target := range []string{findRepoRoot(t), filepath.Join(findRepoRoot(t), "internal"), filepath.Join(t.TempDir(), "missing")} {
		lane, tasks := seedSDKBoard(t, st, target)
		status, _, raw := boardLaunch(t, ts.URL, lane.ID)
		if status != 400 {
			t.Fatalf("target %s: %d %s", target, status, raw)
		}
		ticket, _ := st.GetTicket(t.Context(), tasks[0].ID)
		if ticket.Status != "todo" || ticket.SDKRunID != "" {
			t.Fatalf("unsafe target claimed: %+v", ticket)
		}
	}
	lane, _ := seedSDKBoard(t, st, t.TempDir())
	for _, body := range []map[string]any{{"count": 2}, {"review": true}, {"maxRetries": 1}, {"engine": "codex-local"}} {
		status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/lanes/"+lane.ID+"/work", body)
		if status != 400 {
			t.Fatalf("flags %v: %d %s", body, status, raw)
		}
	}
	if data, _ := os.ReadFile(calls); len(data) != 0 {
		t.Fatalf("refused work called SDK: %s", data)
	}
}

func TestSDKBoardShutdownInterruptsLinkedTask(t *testing.T) {
	setupSDKBoard(t, "hang")
	srv, ts := newTestServer(t, nil)
	st := openStore(t)
	lane, tasks := seedSDKBoard(t, st, t.TempDir())
	status, id, raw := boardLaunch(t, ts.URL, lane.ID)
	if status != 202 {
		t.Fatalf("%d %s", status, raw)
	}
	if !srv.DrainRuns(10 * time.Second) {
		t.Fatal("shutdown failed")
	}
	run := waitSDKBoardRun(t, st, id)
	ticket, err := st.GetTicket(t.Context(), tasks[0].ID)
	if run.State != "interrupted" || err != nil || ticket.Status != "blocked" {
		t.Fatalf("shutdown receipt %s %+v %v", run.State, ticket, err)
	}
}

func TestSDKBoardOverlappingLanesDoNotClaimSecondTask(t *testing.T) {
	setupSDKBoard(t, "hang")
	srv, ts := newTestServer(t, nil)
	st := openStore(t)
	target := t.TempDir()
	first, _ := seedSDKBoard(t, st, target)
	second, tasks := seedSDKBoard(t, st, target)
	status, _, raw := boardLaunch(t, ts.URL, first.ID)
	if status != 202 {
		t.Fatalf("first: %d %s", status, raw)
	}
	status, _, raw = boardLaunch(t, ts.URL, second.ID)
	if status != 409 {
		t.Fatalf("overlap: %d %s", status, raw)
	}
	task, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil || task.Status != "todo" || task.SDKRunID != "" {
		t.Fatalf("overlap claimed task: %+v %v", task, err)
	}
	if !srv.DrainRuns(10 * time.Second) {
		t.Fatal("overlapping writer failed to drain")
	}
}

func TestSDKBoardTransportFailureBlocksWithoutRetry(t *testing.T) {
	calls := setupSDKBoard(t, "transport")
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	lane, tasks := seedSDKBoard(t, st, t.TempDir())
	status, id, raw := boardLaunch(t, ts.URL, lane.ID)
	if status != 202 {
		t.Fatalf("admission: %d %s", status, raw)
	}
	run := waitSDKBoardRun(t, st, id)
	task, err := st.GetTicket(t.Context(), tasks[0].ID)
	if run.State != "failed" || err != nil || task.Status != "blocked" || task.Error == "" {
		t.Fatalf("failed run not linked: %s %+v %v", run.State, task, err)
	}
	data, _ := os.ReadFile(calls)
	if strings.Count(string(data), "start\n") != 1 || strings.Contains(string(data), "resume") {
		t.Fatalf("retried failed call: %s", data)
	}
}

// Completing when the handler first reads its body deterministically places the
// terminal commit between its saved ticket snapshot and its attempted update.
type boardCompletionReader struct {
	io.Reader
	complete func()
}

func (r *boardCompletionReader) Read(p []byte) (int, error) {
	if r.complete != nil {
		r.complete()
		r.complete = nil
	}
	return r.Reader.Read(p)
}

func TestSDKBoardStalePatchCannotRestoreRunningTask(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	st := openStore(t)
	lane, tasks := seedSDKBoard(t, st, t.TempDir())
	source, err := st.NextReadySDKTicket(t.Context(), lane.ID)
	if err != nil {
		t.Fatal(err)
	}
	runID := lane.ID + "-run"
	owner, err := st.OwnSDKRun(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	run := store.Run{RunID: runID, DAG: "checked-task", Goal: source.Ticket.Title, Project: lane.Project, State: "running", Mode: "run", Type: "sdk-task", StartedAt: time.Now(), OwnerPID: os.Getpid(), Options: store.JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"%s","ownerSession":%d,"ticketId":%q,"request":{"workflow":"checked-task"}}`, store.SDKOwnership, owner.SessionID(), tasks[0].ID))}
	if _, err := owner.StartTicket(t.Context(), source, run, store.JSON(`{"phase":"starting"}`)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	patchBody := func(ticket *store.Ticket) string {
		return fmt.Sprintf(`{"assignee":"human","expectedRunId":%q,"expectedUpdatedAt":%q}`, ticket.SDKRunID, ticket.UpdatedAt.Format(time.RFC3339Nano))
	}
	body := &boardCompletionReader{Reader: strings.NewReader(patchBody(snapshot)), complete: func() {
		if _, _, err := owner.SaveCheckpoint(t.Context(), runID, lane.Project, run.OwnerPID, store.JSON(`{"phase":"passed","text":"Saved completed result"}`), "passed", nil); err != nil {
			t.Fatal(err)
		}
		owner.Close()
	}}
	request := httptest.NewRequest(http.MethodPatch, "/api/tickets/"+tasks[0].ID, body)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale patch: %d %s", response.Code, response.Body.String())
	}
	ticket, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil || ticket.Status != "review" || ticket.Output != "Saved completed result" || ticket.SDKRunID != runID || ticket.Assignee == "human" {
		t.Fatalf("terminal evidence overwritten: %+v %v", ticket, err)
	}
	current, err := st.GetRun(t.Context(), runID)
	if err != nil || current.State != "passed" {
		t.Fatalf("terminal run changed: %+v %v", current, err)
	}
	// A client that read before completion must also be rejected when its
	// request arrives after completion, as must an unversioned managed edit.
	for _, raw := range []string{patchBody(snapshot), `{"status":"done"}`} {
		response = httptest.NewRecorder()
		late := httptest.NewRequest(http.MethodPatch, "/api/tickets/"+tasks[0].ID, strings.NewReader(raw))
		late.RemoteAddr = "127.0.0.1:12345"
		srv.Handler().ServeHTTP(response, late)
		if response.Code != http.StatusConflict {
			t.Fatalf("late review accepted: %d %s", response.Code, response.Body.String())
		}
	}

	// A fresh edit remains usable after the conflict.
	response = httptest.NewRecorder()
	freshRequest := httptest.NewRequest(http.MethodPatch, "/api/tickets/"+tasks[0].ID, strings.NewReader(patchBody(ticket)))
	freshRequest.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(response, freshRequest)
	if response.Code != http.StatusOK {
		t.Fatalf("fresh edit rejected: %d %s", response.Code, response.Body.String())
	}
	ticket, err = st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil || ticket.Status != "review" || ticket.Output != "Saved completed result" || ticket.Assignee != "human" {
		t.Fatalf("fresh partial edit corrupted evidence: %+v %v", ticket, err)
	}
}

func TestSDKBoardReviewPinsTheAttemptShownToTheUser(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	st := openStore(t)
	lane, tasks := seedSDKBoard(t, st, t.TempDir())
	var previous *store.Ticket
	for attempt := 1; attempt <= 2; attempt++ {
		source, err := st.NextReadySDKTicket(t.Context(), lane.ID)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("%s-run-%d", lane.ID, attempt)
		owner, err := st.OwnSDKRun(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		run := store.Run{RunID: id, DAG: "checked-task", Goal: source.Ticket.Title, Project: lane.Project, State: "running", Mode: "run", Type: "sdk-task", StartedAt: time.Now(), OwnerPID: os.Getpid(), Options: store.JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"%s","ownerSession":%d,"ticketId":%q,"request":{"workflow":"checked-task"}}`, store.SDKOwnership, owner.SessionID(), tasks[0].ID))}
		if _, err := owner.StartTicket(t.Context(), source, run, store.JSON(`{"phase":"starting"}`)); err != nil {
			owner.Close()
			t.Fatal(err)
		}
		if _, _, err := owner.SaveCheckpoint(t.Context(), id, lane.Project, run.OwnerPID, store.JSON(fmt.Sprintf(`{"phase":"passed","text":"Result %d"}`, attempt)), "passed", nil); err != nil {
			owner.Close()
			t.Fatal(err)
		}
		owner.Close()
		ticket, err := st.GetTicket(t.Context(), tasks[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 1 {
			previous = ticket
			if err := st.UpdateTicketStatus(t.Context(), ticket.ID, "todo", "", ticket.Output, ticket.Error, ticket.UpdatedAt); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw := fmt.Sprintf(`{"status":"done","expectedRunId":%q,"expectedUpdatedAt":%q}`, previous.SDKRunID, previous.UpdatedAt.Format(time.RFC3339Nano))
	request := httptest.NewRequest(http.MethodPatch, "/api/tickets/"+tasks[0].ID, strings.NewReader(raw))
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("review of old attempt accepted: %d %s", response.Code, response.Body.String())
	}
	ticket, err := st.GetTicket(t.Context(), tasks[0].ID)
	if err != nil || ticket.Status != "review" || ticket.SDKRunID == previous.SDKRunID || ticket.Output != "Result 2" {
		t.Fatalf("new attempt overwritten: %+v %v", ticket, err)
	}
}
