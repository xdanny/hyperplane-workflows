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
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

func TestWorkflowCheckCatalogExposesCompleteRequirementsWithoutCreatingWork(t *testing.T) {
	_, ts := newTestServer(t, nil)
	_, before := doJSON(t, http.MethodGet, ts.URL+"/api/workflows", nil)
	status, raw := doJSON(t, http.MethodGet, ts.URL+"/api/workflow-checks", nil)
	var response struct {
		Checks []agentchecks.CheckDefinition `json:"checks"`
	}
	if status != 200 || json.Unmarshal(raw, &response) != nil {
		t.Fatalf("catalog: %d %s", status, raw)
	}
	found := false
	for _, check := range response.Checks {
		if check.ID == "data-quality-audit" {
			found = true
			if check.Title == "" || len(check.Description) < 1000 || !strings.Contains(check.Description, "quality-scorecard.json") || len(check.Requires) != 2 || check.ApprovalAfter != "data-quality-rules" {
				t.Fatalf("missing requirements: %+v", check)
			}
		}
	}
	if !found {
		t.Fatal("audit contract missing")
	}
	_, after := doJSON(t, http.MethodGet, ts.URL+"/api/workflows", nil)
	if string(before) != string(after) {
		t.Fatal("catalog read created a workflow")
	}
}

func TestDescribedGeneralTaskSavesRunsAndReportsNoIndependentCheck(t *testing.T) {
	root := findRepoRoot(t)
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal/codex/testdata/plan"))
	t.Setenv("SDK_FIXTURE_MODE", "general-task")
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SDK_FIXTURE_CALLS", calls)
	srv, ts := newTestServer(t, nil)
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/compose", map[string]string{"brief": "Create answer.txt containing 42."})
	var proposed struct {
		Workflow *store.SDKWorkflow `json:"workflow"`
	}
	if status != 200 || json.Unmarshal(raw, &proposed) != nil || proposed.Workflow == nil {
		t.Fatalf("proposal: %d %s", status, raw)
	}
	w := proposed.Workflow
	if len(w.Nodes) != 1 || w.Nodes[0].Kind != "task" || w.Nodes[0].Check != nil || w.Nodes[0].Acceptance != "" || w.Nodes[0].Access != nil {
		t.Fatalf("task gained execution configuration: %s", raw)
	}
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", w)
	if status != 201 {
		t.Fatalf("save: %d %s", status, raw)
	}
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal/codex/testdata/node-task"))
	status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"goal": w.Instruction, "nodes": w.Nodes})
	var launched struct{ RunID string }
	if status != 202 || json.Unmarshal(raw, &launched) != nil {
		t.Fatalf("launch: %d %s", status, raw)
	}
	var detail struct{ Run store.Run }
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launched.RunID, nil)
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Run.State != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !srv.DrainRuns(5*time.Second) || detail.Run.State != "passed" {
		t.Fatalf("completion: %s", raw)
	}
	if !strings.Contains(string(detail.Run.Doc), `"verification":"not_requested"`) {
		t.Fatalf("completion misrepresented checks: %s", detail.Run.Doc)
	}
	base := ts.URL + "/file/" + launched.RunID + "/nodes/delivery/OUTPUTS/"
	status, content := doJSON(t, http.MethodGet, base+"build/answer.txt", nil)
	if status != 200 || string(content) != "42" {
		t.Fatalf("file: %d %s", status, content)
	}
	status, handoff := doJSON(t, http.MethodGet, base+"build.md", nil)
	if status != 200 || !strings.Contains(string(handoff), "No extra acceptance check was configured or run") {
		t.Fatalf("handoff: %d %s", status, handoff)
	}
	actual, _ := os.ReadFile(calls)
	if string(actual) != "build\n" {
		t.Fatalf("unexpected model calls: %q", actual)
	}
}
