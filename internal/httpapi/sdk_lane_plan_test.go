package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func setupLanePlannerFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/tickets/testdata/sdk-plan"))
	calls := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("SDK_TICKET_CALLS", calls)
	t.Setenv("SDK_TICKET_MODE", "ordinary")
	return calls
}

func TestSDKLaneDecomposePublishesCompletePlanFromCurrentRun(t *testing.T) {
	calls := setupLanePlannerFixture(t)
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	source := store.Run{RunID: "lane-source", Goal: "Sales follow-up", Project: "sales", State: "passed", Options: json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"checked-task","instruction":"Calculate sales totals"}}`), Doc: json.RawMessage(`{"sdk":{"phase":"passed","text":"The reported total is 21","verification":"not_requested"},"legacy":{"pick":{"appName":"WRONG GRAPH"}}}`)}
	if err := st.CreateRun(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/lanes/decompose", map[string]any{"fromRun": source.RunID, "targetRoot": "/target"})
	if status != http.StatusCreated {
		t.Fatalf("%d %s", status, raw)
	}
	var result struct {
		LaneID    string   `json:"laneId"`
		TicketIDs []string `json:"ticketIds"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	lane, err := st.GetLane(t.Context(), result.LaneID)
	if err != nil || lane.Project != "sales" || lane.OriginRun != source.RunID || len(result.TicketIDs) != 2 {
		t.Fatalf("lane=%+v ids=%v err=%v", lane, result.TicketIDs, err)
	}
	second, err := st.GetTicket(t.Context(), result.TicketIDs[1])
	if err != nil || len(second.Deps) != 1 || second.Deps[0] != result.TicketIDs[0] {
		t.Fatalf("ticket=%+v err=%v", second, err)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	var call struct {
		Prompt string `json:"prompt"`
		Cwd    string `json:"cwd"`
	}
	if err := json.Unmarshal(data, &call); err != nil {
		t.Fatalf("expected exactly one SDK call: %s", data)
	}
	if !strings.Contains(call.Prompt, "The reported total is 21") || strings.Contains(call.Prompt, "WRONG GRAPH") || strings.Contains(call.Prompt, "Xcode") {
		t.Fatalf("unexpected SDK context: %s", call.Prompt)
	}
	if _, err := os.Stat(call.Cwd); !os.IsNotExist(err) {
		t.Fatalf("planner workspace retained: %v", err)
	}
}

func TestSDKLaneDecomposeRejectsInvalidSourceWithoutModelOrWrites(t *testing.T) {
	calls := setupLanePlannerFixture(t)
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	before, err := st.ListLanes(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(t.Context(), store.Run{RunID: "graph-source", State: "passed", Options: json.RawMessage(`{"spec":{}}`), Doc: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{{"fromRun": "missing"}, {"fromRun": "graph-source"}, {"fromRun": "graph-source", "name": "Conflict"}, {"name": "Plan", "engine": "unknown"}, {"name": "Plan", "unknown": true}} {
		status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/lanes/decompose", body)
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			t.Fatalf("invalid source: %d %s", status, raw)
		}
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("invalid input called model")
	}
	lanes, err := st.ListLanes(t.Context(), nil)
	if err != nil || len(lanes) != len(before) {
		t.Fatalf("invalid source wrote lanes: %v %v", lanes, err)
	}
}

func TestSDKLaneDecomposeReportsPlannerFailureWithoutWrites(t *testing.T) {
	setupLanePlannerFixture(t)
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	before, err := st.ListLanes(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"malformed", "transport"} {
		t.Setenv("SDK_TICKET_MODE", mode)
		status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/lanes/decompose", map[string]any{"name": "Report", "concept": "Sales report", "project": "not-created"})
		if status != http.StatusBadGateway {
			t.Fatalf("%s: %d %s", mode, status, raw)
		}
	}
	lanes, err := st.ListLanes(t.Context(), nil)
	if err != nil || len(lanes) != len(before) {
		t.Fatalf("failed plan wrote lanes: %v %v", lanes, err)
	}
	if _, err := st.GetProject(t.Context(), "not-created"); err == nil {
		t.Fatal("failed plan registered project")
	}
}

func TestSDKLaneDecomposeCancellationOwnsPlanner(t *testing.T) {
	calls := setupLanePlannerFixture(t)
	t.Setenv("SDK_TICKET_MODE", "hang")
	_, ts := newTestServer(t, nil)
	st := openStore(t)
	before, err := st.ListLanes(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/lanes/decompose", strings.NewReader(`{"name":"Canceled"}`))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	var call struct {
		Cwd string `json:"cwd"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(calls); err == nil && json.Unmarshal(data, &call) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if call.Cwd == "" {
		t.Fatal("planner did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(call.Cwd); os.IsNotExist(err) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(call.Cwd); !os.IsNotExist(err) {
		t.Fatal("planner did not drain")
	}
	lanes, err := st.ListLanes(t.Context(), nil)
	if err != nil || len(lanes) != len(before) {
		t.Fatalf("canceled plan wrote lanes: %v %v", lanes, err)
	}
}
