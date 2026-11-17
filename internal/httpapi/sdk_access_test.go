package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKStepAccessRoundTripAndExecution(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/task-access"))
	t.Setenv("WORKFLOW_PROBE", "selected-canary")
	t.Setenv("UNSELECTED_PROBE", "not-forwarded")
	for _, mode := range []string{"ordinary", "missing-connection"} {
		t.Run(mode, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls.json")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			if mode == "missing-connection" {
				t.Setenv("WORKFLOW_PROBE", "")
			}
			_, ts := newTestServer(t, nil)
			source := filepath.Join(t.TempDir(), "source.csv")
			if err := os.WriteFile(source, []byte("id\n1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			nodes := []sdkworkflow.Node{{ID: "build", Kind: "task", Title: "Build answer", Instruction: "Create answer.txt", Check: []string{"python3", "-c", "from pathlib import Path; assert Path('answer.txt').read_text() == '42'"}, Access: &sdkworkflow.TaskAccess{Hosts: []string{"example.com"}, ReadPaths: []string{source}, Environment: []string{"WORKFLOW_PROBE"}}}}
			status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/workflows", store.SDKWorkflow{Name: "Connected work", Kind: "nodes", Instruction: "Produce a checked answer", Nodes: nodes})
			var saved store.SDKWorkflow
			if status != 201 || json.Unmarshal(raw, &saved) != nil || !reflect.DeepEqual(saved.Nodes, nodes) {
				t.Fatalf("save: %d %s", status, raw)
			}
			status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/workflows", nil)
			var listed []store.SDKWorkflow
			if status != 200 || json.Unmarshal(raw, &listed) != nil {
				t.Fatalf("load: %d %s", status, raw)
			}
			found := false
			for _, item := range listed {
				if item.ID == saved.ID && reflect.DeepEqual(item.Nodes, nodes) {
					found = true
				}
			}
			if !found {
				t.Fatalf("saved access was not retained: %s", raw)
			}
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": saved.Instruction, "nodes": saved.Nodes})
			var launch struct{ RunID string }
			if status != 202 || json.Unmarshal(raw, &launch) != nil {
				t.Fatalf("launch: %d %s", status, raw)
			}
			var detail struct{ Run store.Run }
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Run.State == "passed" || detail.Run.State == "failed" {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			want := "passed"
			if mode != "ordinary" {
				want = "failed"
			}
			if detail.Run.State != want {
				t.Fatalf("state=%s want=%s: %s", detail.Run.State, want, raw)
			}
			if mode == "ordinary" {
				var receipt struct {
					Runtime                           string
					SelectedPresent, UnselectedAbsent bool
				}
				data, err := os.ReadFile(calls)
				if err != nil || json.Unmarshal(data, &receipt) != nil || !receipt.SelectedPresent || !receipt.UnselectedAbsent {
					t.Fatalf("receipt: %s %v", data, err)
				}
				if _, err := os.Stat(receipt.Runtime); !os.IsNotExist(err) {
					t.Fatalf("tool runtime remains: %v", err)
				}
			} else if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatalf("missing connection reached SDK: %v", err)
			}
		})
	}
}
