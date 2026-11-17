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

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKMLTemplateLifecycle(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/prediction-metrics"))
	for _, mode := range []string{"pass", "wrong-value", "wrong-holdout", "missed-control", "no-execution", "review-reject", "human-reject"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			srv, ts := newTestServer(t, nil)
			st := openStore(t)
			var template store.WorkflowTemplate
			for _, candidate := range seed.DefaultTemplates {
				if candidate.Name == "ml-engineering" {
					template = candidate
				}
			}
			template.Name += "-sdk-" + mode
			old, err := st.CreateTemplate(context.Background(), store.WorkflowTemplate{Name: template.Name, Spec: store.JSON(`{"name":"retired","nodes":[]}`)})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.RefreshTemplateByName(context.Background(), template); err != nil {
				t.Fatal(err)
			}
			slots := map[string]string{"task": "Evaluate a synthetic binary baseline", "predictionTarget": "Binary outcome known after observation", "dataDescription": "holdout sha256 " + strings.Repeat("ab", 32), "splitPolicy": "Development and evaluation rows have disjoint IDs", "successMetric": `[{"metric":"AUROC","threshold":">= 0.72"}]`}
			endpoint := ts.URL + "/api/templates/" + old.ID + "/instantiate"
			before := storedDraftSnapshot(t)
			status, raw := doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": slots})
			var prepared struct {
				Workflow store.SDKWorkflow
				Draft    json.RawMessage
			}
			if status != 201 || json.Unmarshal(raw, &prepared) != nil || prepared.Workflow.Kind != "nodes" || len(prepared.Workflow.Nodes) != 7 || len(prepared.Draft) != 0 {
				t.Fatalf("prepare: %d %s", status, raw)
			}
			if string(before) != string(storedDraftSnapshot(t)) {
				t.Fatal("ML preparation created a graph draft")
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("preparation invoked model")
			}
			if prepared.Workflow.Nodes[3].Acceptance != "prediction-metrics" {
				t.Fatal("trusted metric check missing")
			}
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", prepared.Workflow)
			var saved store.SDKWorkflow
			if status != 201 || json.Unmarshal(raw, &saved) != nil {
				t.Fatalf("save: %d %s", status, raw)
			}
			// The saved workflow keeps its questions; this run supplies the answers.
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"sourceWorkflowId": saved.ID, "revision": saved.Revision, "slots": slots})
			var launched struct{ RunID string }
			if status != 202 || json.Unmarshal(raw, &launched) != nil {
				t.Fatalf("launch: %d %s", status, raw)
			}
			var detail struct{ Run store.Run }
			acted := false
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launched.RunID, nil)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
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
					if strings.Contains(string(history), "baseline") {
						t.Fatal("baseline ran before approval")
					}
					found := false
					for _, file := range doc.SDK.Approval.Documents {
						if file.Name == "plan/plan.md" {
							found = true
							status, _ := doJSON(t, http.MethodGet, ts.URL+file.URL, nil)
							if status != 200 {
								t.Fatal("plan file unreadable")
							}
						}
					}
					if !found {
						t.Fatal("approval omitted baseline plan")
					}
					decision := "approved"
					if mode == "human-reject" {
						decision = "rejected"
					}
					status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/approvals/"+doc.SDK.Approval.ID+"/decision", map[string]string{"decision": decision})
					if status != 200 {
						t.Fatalf("decision: %d %s", status, raw)
					}
					acted = true
				}
				if detail.Run.State != "running" {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !srv.DrainRuns(5 * time.Second) {
				t.Fatal("ML run did not drain")
			}
			want := "failed"
			if mode == "pass" {
				want = "passed"
			}
			if detail.Run.State != want || !acted {
				t.Fatalf("completion %s want %s: %s", detail.Run.State, want, raw)
			}
			history, _ := os.ReadFile(calls)
			if mode == "human-reject" && strings.Contains(string(history), "baseline") {
				t.Fatal("rejected plan trained baseline")
			}
			if mode == "pass" {
				status, score := doJSON(t, http.MethodGet, ts.URL+"/file/"+launched.RunID+"/nodes/delivery/OUTPUTS/baseline/score-report.json", nil)
				var report struct {
					Value             float64
					PredictionsSha256 string
				}
				if status != 200 || json.Unmarshal(score, &report) != nil || report.Value != 0.75 || len(report.PredictionsSha256) != 64 {
					t.Fatalf("trusted score missing: %d %s", status, score)
				}
				if strings.Count(string(history), "baseline\n") != 1 || !strings.Contains(string(history), "card\n") {
					t.Fatalf("unexpected SDK sequence: %s", history)
				}
			} else if mode != "review-reject" && strings.Contains(string(history), "card\n") {
				t.Fatal("rejected evidence reached model card")
			}
		})
	}
}
