package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKResearchTemplateReplacesNestedProcesses(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/research"))
	for _, mode := range []string{"ordinary", "direct", "reject", "invalid", "no-search", "review-no-search", "hang"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "direct" {
				fixtureMode = "ordinary"
			}
			t.Setenv("SDK_FIXTURE_MODE", fixtureMode)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			srv, ts := newTestServer(t, nil)
			var template store.WorkflowTemplate
			for _, candidate := range seed.DefaultTemplates {
				if candidate.Name == "market-research" {
					template = candidate
				}
			}
			if len(template.Spec) == 0 {
				t.Fatal("SDK market-research template missing")
			}
			template.Name += "-" + mode
			status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/templates", template)
			if status != 201 || json.Unmarshal(raw, &template) != nil {
				t.Fatalf("template: %d %s", status, raw)
			}
			endpoint := ts.URL + "/api/templates/" + template.ID + "/instantiate"
			beforeDrafts := storedDraftSnapshot(t)
			status, raw = doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": map[string]string{"market": "Cycling tools"}})
			if status != 400 {
				t.Fatalf("missing audience: %d %s", status, raw)
			}
			for _, extra := range []map[string]any{{"dryRun": true}, {"goal": "Legacy label override"}} {
				extra["slots"] = map[string]string{"market": "Cycling tools", "audience": "Weekend cyclists"}
				status, raw = doJSON(t, http.MethodPost, endpoint, extra)
				if status != 400 {
					t.Fatalf("unsupported launch setting: %d %s", status, raw)
				}
			}
			status, raw = doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": map[string]string{"market": "Cycling tools", "audience": "Weekend cyclists", "competitorsHint": "Known tool"}, "project": "sdk-market"})
			var result struct {
				Workflow store.SDKWorkflow `json:"workflow"`
				Draft    json.RawMessage   `json:"draft"`
			}
			if status != 201 || json.Unmarshal(raw, &result) != nil || result.Workflow.Kind != "nodes" || len(result.Workflow.Nodes) != 4 || len(result.Draft) > 0 {
				t.Fatalf("SDK preparation: %d %s", status, raw)
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("preparation made a model call")
			}
			afterDrafts := storedDraftSnapshot(t)
			if string(beforeDrafts) != string(afterDrafts) {
				t.Fatal("SDK template created a graph draft")
			}
			// Preparation keeps the template's questions instead of writing this
			// run's answers into the prose, so the saved result runs again with
			// new values rather than being edited in several places.
			if !strings.Contains(result.Workflow.Instruction, "{slot:audience}") || len(result.Workflow.Inputs) == 0 {
				t.Fatalf("lost template input declarations: %+v", result.Workflow)
			}
			result.Workflow.Nodes = append(result.Workflow.Nodes, sdkworkflow.Node{ID: "after", Title: "After review", Instruction: "Continue only if the independent review passes"})
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", result.Workflow)
			var saved store.SDKWorkflow
			if status != 201 || json.Unmarshal(raw, &saved) != nil {
				t.Fatalf("save: %d %s", status, raw)
			}
			wantLaunch := 202
			if mode == "direct" {
				status, raw = doJSON(t, http.MethodPost, endpoint, map[string]any{"run": true, "slots": map[string]string{"market": "Cycling tools", "audience": "Weekend cyclists"}, "project": "sdk-market"})
				wantLaunch = 201
			} else {
				status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"sourceWorkflowId": saved.ID, "revision": saved.Revision,
					"slots": map[string]string{"market": "Cycling tools", "audience": "Weekend cyclists", "competitorsHint": "Known tool"}})
			}
			var launch struct {
				RunID string `json:"runId"`
			}
			if status != wantLaunch || json.Unmarshal(raw, &launch) != nil {
				t.Fatalf("launch: %d %s", status, raw)
			}
			var detail struct {
				Run   store.Run    `json:"run"`
				Nodes []store.Node `json:"nodes"`
			}
			cancelled := false
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
				if mode == "hang" && !cancelled && strings.Contains(string(detail.Run.Doc), "fixture-review") {
					status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/runs/"+launch.RunID+"/cancel", nil)
					if status != 200 {
						t.Fatalf("cancel: %d %s", status, raw)
					}
					cancelled = true
				}
				if detail.Run.State != "running" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "failed"
			if mode == "ordinary" || mode == "direct" {
				want = "passed"
			}
			if mode == "hang" {
				want = "interrupted"
			}
			if !srv.DrainRuns(5*time.Second) || detail.Run.State != want {
				t.Fatalf("state %s want %s: %s", detail.Run.State, want, detail.Run.Doc)
			}
			if len(detail.Nodes) != 1 || detail.Nodes[0].Kind != "sdk" || !strings.Contains(string(detail.Run.Options), "codex-sdk") {
				t.Fatalf("used graph execution: %s", raw)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), `"stage": "after"`) != (mode == "ordinary") {
				t.Fatalf("wrong continuation: %s", data)
			}
			if mode == "ordinary" && (!strings.Contains(string(detail.Run.Doc), "fixture-review") || !strings.Contains(string(detail.Run.Doc), "Fixture caveat retained")) {
				t.Fatal("lost independent receipt or caveat")
			}
			if mode == "ordinary" || mode == "reject" {
				status, raw = doJSON(t, http.MethodGet, ts.URL+"/file/"+launch.RunID+"/nodes/delivery/OUTPUTS/review.md", nil)
				if status != 200 || !strings.Contains(string(raw), "Model review:") {
					t.Fatalf("review receipt: %d %s", status, raw)
				}
			}
		})
	}
}
