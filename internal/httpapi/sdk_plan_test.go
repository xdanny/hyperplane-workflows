package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKPlanProposesEditableNodesWithoutSavingOrRunning(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/plan"))
	for _, mode := range []string{"ordinary", "unsupported", "partial", "duplicate", "unknown", "task", "access", "toolchain", "no-findings", "transport", "builtin-pass", "builtin-command", "builtin-access", "builtin-unknown", "builtin-missing", "builtin-extra-input", "builtin-out-of-order", "builtin-no-review", "builtin-no-final-review", "builtin-invented-source", "builtin-missing-facts"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			_, ts := newTestServer(t, nil)
			beforeRuns, rawRuns := doJSON(t, http.MethodGet, ts.URL+"/api/runs", nil)
			beforeWorkflows, rawWorkflows := doJSON(t, http.MethodGet, ts.URL+"/api/workflows", nil)
			status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/compose", map[string]any{"brief": "Audit /declared/source.csv with id keys and zero negative amounts; counts only", "project": "sdk-proposal"})
			data, _ := os.ReadFile(calls)
			if string(data) != "plan\n" {
				t.Fatalf("expected one SDK call: %q", data)
			}
			want := 200
			if mode == "duplicate" || mode == "unknown" || mode == "task" || mode == "access" || mode == "toolchain" || mode == "no-findings" || mode == "transport" {
				want = 502
			}
			if strings.HasPrefix(mode, "builtin-") && mode != "builtin-pass" && mode != "builtin-missing-facts" {
				want = 502
			}
			if status != want {
				t.Fatalf("status: %d want %d: %s", status, want, raw)
			}
			if status == 200 {
				var result struct {
					Workflow    *store.SDKWorkflow `json:"workflow"`
					Unsupported []string           `json:"unsupported"`
				}
				if err := json.Unmarshal(raw, &result); err != nil {
					t.Fatal(err)
				}
				if mode == "unsupported" || mode == "builtin-missing-facts" {
					if result.Workflow != nil || len(result.Unsupported) != 1 {
						t.Fatalf("unsupported request lost: %s", raw)
					}
				} else if mode == "builtin-pass" {
					if result.Workflow == nil || len(result.Workflow.Nodes) != 9 || result.Workflow.Nodes[0].Inputs["dataset"] != "/declared/source.csv" || result.Workflow.Nodes[5].Acceptance != "data-quality-audit" {
						t.Fatalf("checked proposal lost: %s", raw)
					}
				} else {
					if result.Workflow == nil || result.Workflow.Kind != "nodes" || result.Workflow.ID != "" || result.Workflow.Revision != 0 || result.Workflow.Project != "sdk-proposal" || len(result.Workflow.Nodes) != 2 || result.Workflow.Nodes[1].Instruction != "Read explore.md and compare the ideas." {
						t.Fatalf("not editable SDK settings: %s", raw)
					}
					if mode == "partial" && len(result.Unsupported) != 1 {
						t.Fatalf("partial proposal concealed missing actions: %s", raw)
					}
				}
			}
			afterRuns, newRuns := doJSON(t, http.MethodGet, ts.URL+"/api/runs", nil)
			afterWorkflows, newWorkflows := doJSON(t, http.MethodGet, ts.URL+"/api/workflows", nil)
			if beforeRuns != afterRuns || string(rawRuns) != string(newRuns) || beforeWorkflows != afterWorkflows || string(rawWorkflows) != string(newWorkflows) {
				t.Fatal("proposing changed saved workflows or started a run")
			}
		})
	}
}

func TestSDKProposedCheckedWorkflowSavesAndRunsAfterApproval(t *testing.T) {
	root := findRepoRoot(t)
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal/codex/testdata/plan"))
	t.Setenv("SDK_FIXTURE_MODE", "builtin-pass")
	data := filepath.Join(t.TempDir(), "source.csv")
	original := []byte("id,amount\n1,10\n2,-2\n2,3\n4,0\n")
	if err := os.WriteFile(data, original, 0600); err != nil {
		t.Fatal(err)
	}
	inputs, _ := json.Marshal(map[string]string{"dataset": data, "keyColumns": "id", "expectations": "IDs are unique; amounts are nonnegative", "grain": "one invoice row", "safeUse": "counts only"})
	t.Setenv("SDK_FIXTURE_INPUTS", string(inputs))
	t.Setenv("SDK_FIXTURE_DATA", data)
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SDK_FIXTURE_CALLS", calls)
	_, ts := newTestServer(t, nil)
	goal := "Audit " + data + " with id keys. IDs must be unique and amounts nonnegative. One invoice per row; counts only."
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/compose", map[string]any{"brief": goal})
	var proposed struct{ Workflow store.SDKWorkflow }
	if status != 200 || json.Unmarshal(raw, &proposed) != nil {
		t.Fatalf("propose: %d %s", status, raw)
	}
	history, _ := os.ReadFile(calls)
	if string(history) != "plan\n" {
		t.Fatalf("proposal executed work: %s", history)
	}
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", proposed.Workflow)
	var saved store.SDKWorkflow
	if status != 201 || json.Unmarshal(raw, &saved) != nil {
		t.Fatalf("save: %d %s", status, raw)
	}
	if saved.Nodes[0].Inputs["dataset"] != data || saved.Nodes[5].Acceptance != "data-quality-audit" {
		t.Fatal("saved proposal changed")
	}
	// An explicit caller launch starts actual SDK transport with deterministic
	// model responses; this is not a live model-quality proof.
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal/codex/testdata/data-quality"))
	t.Setenv("SDK_FIXTURE_MODE", "pass")
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": saved.Instruction, "nodes": saved.Nodes})
	var launch struct{ RunID string }
	if status != 202 || json.Unmarshal(raw, &launch) != nil {
		t.Fatalf("launch: %d %s", status, raw)
	}
	acted := false
	state := ""
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
		var detail struct{ Run store.Run }
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		state = detail.Run.State
		var doc struct {
			SDK struct {
				Phase    string
				Approval *struct {
					ID        string
					Documents []struct{ Name, URL string }
				}
			}
		}
		if err := json.Unmarshal(detail.Run.Doc, &doc); err != nil {
			t.Fatal(err)
		}
		if !acted && doc.SDK.Phase == "approval_pending" && doc.SDK.Approval != nil {
			history, _ := os.ReadFile(calls)
			if strings.Contains(string(history), "audit") {
				t.Fatal("audit preceded approval")
			}
			found := false
			for _, file := range doc.SDK.Approval.Documents {
				if file.Name == "rules/quality-rules.json" {
					found = true
					response, err := http.Get(ts.URL + file.URL)
					if err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
					if response.StatusCode != 200 {
						t.Fatal("reviewed rules inaccessible")
					}
				}
			}
			if !found {
				t.Fatal("no rules to approve")
			}
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/approvals/"+doc.SDK.Approval.ID+"/decision", map[string]any{"decision": "approved"})
			if status != 200 {
				t.Fatalf("approve: %d %s", status, raw)
			}
			acted = true
		}
		if state == "passed" || state == "failed" || state == "cancelled" {
			break
		}
	}
	if state != "passed" || !acted {
		t.Fatalf("proposed workflow did not finish after approval: %s %s", state, raw)
	}
	history, _ = os.ReadFile(calls)
	if !strings.Contains(string(history), "audit\nreview\nsummary\nreview\n") {
		t.Fatalf("missing checked delivery: %s", history)
	}
	current, _ := os.ReadFile(data)
	if string(current) != string(original) {
		t.Fatal("original source changed")
	}
}
