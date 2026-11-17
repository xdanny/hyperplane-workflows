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

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKDataQualityRecipe(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/data-quality"))
	for _, mode := range []string{"pass", "connected", "no-execution", "bad-counts", "missing-rule", "missed-negative", "dropped-expectation", "review-reject", "human-reject", "missing-key", "explicit-budget"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			t.Setenv("WORKFLOW_PROBE", "selected-canary")
			t.Setenv("HYPERPLANE_RUN_TOKEN_BUDGET", "")
			if mode == "explicit-budget" {
				t.Setenv("HYPERPLANE_RUN_TOKEN_BUDGET", "1")
			}
			data := filepath.Join(t.TempDir(), "source.csv")
			input := []byte("id,amount\n1,10\n2,-2\n2,3\n4,0\n")
			if err := os.WriteFile(data, input, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SDK_FIXTURE_DATA", data)
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			_, ts := newTestServer(t, nil)
			st, err := store.Open(context.Background(), os.Getenv("DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(st.Close)
			var template store.WorkflowTemplate
			for _, candidate := range seed.DefaultTemplates {
				if candidate.Name == "data-quality-audit" {
					template = candidate
				}
			}
			template.Name += "-sdk-" + mode
			legacy, err := st.CreateTemplate(context.Background(), store.WorkflowTemplate{Name: template.Name, Spec: store.JSON(`{"name":"old","nodes":[]}`)})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.RefreshTemplateByName(context.Background(), template); err != nil {
				t.Fatal(err)
			}
			values := map[string]string{"dataset": data, "expectations": "IDs are unique; amounts are nonnegative", "grain": "one invoice row", "keyColumns": "id", "safeUse": "counts only"}
			if mode == "missing-key" {
				values["keyColumns"] = "missing"
			}
			endpoint := ts.URL + "/api/templates/" + legacy.ID + "/instantiate"
			for _, slot := range template.ContextSlots {
				if slot.Required {
					original := values[slot.ID]
					delete(values, slot.ID)
					status, raw := doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": values})
					if status != 400 {
						t.Fatalf("missing %s: %d %s", slot.ID, status, raw)
					}
					values[slot.ID] = original
				}
			}
			before := storedDraftSnapshot(t)
			status, raw := doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": values})
			var prepared struct{ Workflow store.SDKWorkflow }
			if status != 201 || json.Unmarshal(raw, &prepared) != nil || len(prepared.Workflow.Nodes) != 9 {
				t.Fatalf("prepare: %d %s", status, raw)
			}
			after := storedDraftSnapshot(t)
			if string(before) != string(after) {
				t.Fatal("SDK preparation created a graph draft")
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("preparation ran the model")
			}
			if prepared.Workflow.Nodes[0].Inputs["dataset"] != "{slot:dataset}" || prepared.Workflow.Nodes[0].Inputs["keyColumns"] != "{slot:keyColumns}" || len(prepared.Workflow.Inputs) == 0 {
				t.Fatal("preparation baked this run's answers into the steps instead of keeping reusable inputs")
			}
			if mode == "connected" {
				for _, i := range []int{0, 5} {
					prepared.Workflow.Nodes[i].Access = &sdkworkflow.TaskAccess{Hosts: []string{"example.com"}, ReadPaths: []string{data}, Environment: []string{"WORKFLOW_PROBE"}}
				}
			}
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", prepared.Workflow)
			var saved store.SDKWorkflow
			if status != 201 || json.Unmarshal(raw, &saved) != nil || saved.Nodes[5].Acceptance != "data-quality-audit" {
				t.Fatalf("save: %d %s", status, raw)
			}
			// The saved workflow keeps its questions; this run supplies the answers.
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"sourceWorkflowId": saved.ID, "revision": saved.Revision, "slots": values})
			var launch struct{ RunID string }
			if status != 202 || json.Unmarshal(raw, &launch) != nil {
				t.Fatalf("launch: %d %s", status, raw)
			}
			var detail struct {
				Run   store.Run
				Nodes []store.Node
			}
			acted := false
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
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
					if strings.Contains(string(history), "audit") {
						t.Fatal("audit ran before approval")
					}
					found := false
					for _, file := range doc.SDK.Approval.Documents {
						if file.Name == "rules/quality-rules.json" {
							found = true
							res, e := http.Get(ts.URL + file.URL)
							if e != nil {
								t.Fatal(e)
							}
							res.Body.Close()
							if res.StatusCode != 200 {
								t.Fatal("approved file unreadable")
							}
						}
					}
					if !found {
						t.Fatal("approval omitted rule file")
					}
					decision := "approved"
					if mode == "human-reject" {
						decision = "rejected"
					}
					status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/approvals/"+doc.SDK.Approval.ID+"/decision", map[string]any{"decision": decision})
					if status != 200 {
						t.Fatalf("decision: %d %s", status, raw)
					}
					acted = true
				}
				if detail.Run.State == "passed" || detail.Run.State == "failed" || detail.Run.State == "cancelled" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "failed"
			if mode == "pass" || mode == "connected" {
				want = "passed"
			}
			if detail.Run.State != want {
				t.Fatalf("state %s want %s: %s", detail.Run.State, want, raw)
			}
			history, _ := os.ReadFile(calls)
			if mode == "missing-key" && len(history) != 0 {
				t.Fatal("bad source key reached model")
			}
			if mode != "pass" && mode != "connected" && strings.Contains(string(history), "summary") {
				t.Fatal("failed audit reached summary")
			}
			if (mode == "pass" || mode == "connected") && (!acted || !strings.Contains(string(history), "audit\nreview\nsummary\nreview")) {
				t.Fatalf("incomplete SDK sequence: %s", history)
			}
			var limits struct{ MaxTokens int }
			if err := json.Unmarshal(detail.Run.Options, &limits); err != nil {
				t.Fatal(err)
			}
			wantBudget := 9 * 300000
			if mode == "explicit-budget" {
				wantBudget = 1
			}
			if limits.MaxTokens != wantBudget {
				t.Fatalf("workflow budget lost: %+v", limits)
			}
			if len(detail.Nodes) != 1 || detail.Nodes[0].NodeID != "delivery" {
				t.Fatalf("not one SDK delivery: %+v", detail.Nodes)
			}
			current, _ := os.ReadFile(data)
			if string(current) != string(input) {
				t.Fatal("source data changed")
			}
		})
	}
}
