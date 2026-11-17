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

func TestSDKDataPipelineRecipe(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/data-pipeline"))
	for _, mode := range []string{"pass", "connected", "direct-output", "partial-source", "missing-control", "missing-output", "wrong-key", "no-execution", "source-drift", "wrong-counts", "wrong-delta", "relaxed-tolerance", "negative-passes", "leaked-staging", "replay-drift", "review-reject", "delivery-review-reject", "human-reject", "missing-source", "explicit-budget"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", mode)
			t.Setenv("WORKFLOW_PROBE", "selected-canary")
			t.Setenv("HYPERPLANE_RUN_TOKEN_BUDGET", "")
			if mode == "explicit-budget" {
				t.Setenv("HYPERPLANE_RUN_TOKEN_BUDGET", "1")
			}
			data := filepath.Join(t.TempDir(), "orders.csv")
			input := []byte("id,ordered_at,amount,is_test,customer_email\n1,2026-09-01T12:00:00Z,10.25,0,a@example.test\n2,2026-09-01T15:00:00Z,20.50,0,b@example.test\n3,2026-09-02T12:00:00Z,5.25,0,c@example.test\n4,2026-09-02T12:00:00Z,999.00,1,d@example.test\n")
			if err := os.WriteFile(data, input, 0600); err != nil {
				t.Fatal(err)
			}

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
				if candidate.Name == "data-pipeline" {
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
			values := map[string]string{"sources": data, "targetStore": "SQLite database under delivery/", "grain": "one row per UTC order date", "keyColumns": "order_day", "measures": `[{"measure":"net","definition":"Sum order amounts excluding test orders","unit":"exact","tolerance":"0"}]`, "maxLag": "0 days behind the static source", "safeUse": "Never deliver customer email"}
			if mode == "missing-source" {
				values["sources"] = filepath.Join(t.TempDir(), "missing.csv")
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
			if status != 201 || json.Unmarshal(raw, &prepared) != nil || len(prepared.Workflow.Nodes) != 7 {
				t.Fatalf("prepare: %d %s", status, raw)
			}
			after := storedDraftSnapshot(t)
			if string(before) != string(after) {
				t.Fatal("SDK preparation created a graph draft")
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("preparation ran the model")
			}
			if prepared.Workflow.Nodes[0].Inputs["sources"] != "{slot:sources}" || prepared.Workflow.Nodes[0].Inputs["keyColumns"] != "{slot:keyColumns}" || len(prepared.Workflow.Inputs) == 0 {
				t.Fatal("preparation baked this run's answers into the steps instead of keeping reusable inputs")
			}
			if mode == "connected" {
				for _, i := range []int{0, 4} {
					prepared.Workflow.Nodes[i].Access = &sdkworkflow.TaskAccess{Hosts: []string{"example.com"}, ReadPaths: []string{data}, Environment: []string{"WORKFLOW_PROBE"}}
				}
			}
			status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", prepared.Workflow)
			var saved store.SDKWorkflow
			if status != 201 || json.Unmarshal(raw, &saved) != nil || saved.Nodes[4].Acceptance != "data-pipeline-audit" {
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
						if file.Name == "rules/pipeline-plan.json" {
							found = true
							res, e := http.Get(ts.URL + file.URL)
							if e != nil {
								t.Fatal(e)
							}
							var plan struct {
								OutputModel string
								Models      []struct {
									Name      string
									Upstreams []struct{ Kind, Name string }
								}
							}
							decodeErr := json.NewDecoder(res.Body).Decode(&plan)
							res.Body.Close()
							if decodeErr != nil || plan.OutputModel != "daily_sales" {
								t.Fatalf("approved output was not presented: %+v %v", plan, decodeErr)
							}
							if mode == "direct-output" && (len(plan.Models) != 1 || len(plan.Models[0].Upstreams) != 1 || plan.Models[0].Upstreams[0].Kind != "source" || plan.Models[0].Upstreams[0].Name != "orders") {
								t.Fatalf("direct source-to-output plan was changed: %+v", plan)
							}
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
			if mode == "pass" || mode == "connected" || mode == "direct-output" {
				want = "passed"
			}
			if detail.Run.State != want {
				t.Fatalf("state %s want %s: %s", detail.Run.State, want, raw)
			}
			expectedFinding := map[string]string{
				"partial-source": "verified full scan", "missing-control": "Independently measure every requested measure", "missing-output": "Name one approved output", "wrong-key": "keys must exist", "no-execution": "Run exactly sh checks.sh", "source-drift": "same full source snapshot", "wrong-counts": "retained source contributions", "wrong-delta": "exact observed-minus-control delta", "relaxed-tolerance": "Preserve each declared measure", "negative-passes": "injected duplicate", "leaked-staging": "only the approved output model", "replay-drift": "preserve row count", "review-reject": "Fixture reviewer rejected", "delivery-review-reject": "Fixture reviewer rejected", "human-reject": "Human rejected", "missing-source": "declared source file cannot be read", "explicit-budget": "budget",
			}[mode]
			if expectedFinding != "" && !strings.Contains(string(raw), expectedFinding) {
				t.Fatalf("failed for the wrong reason; want %q: %s", expectedFinding, raw)
			}
			history, _ := os.ReadFile(calls)
			if mode == "missing-source" && len(history) != 0 {
				t.Fatal("unreadable source reached model")
			}
			if mode != "pass" && mode != "connected" && mode != "direct-output" && mode != "delivery-review-reject" && strings.Contains(string(history), "summary") {
				t.Fatal("failed audit reached summary")
			}
			if (mode == "pass" || mode == "connected" || mode == "direct-output") && (!acted || !strings.Contains(string(history), "audit\nsummary\nreview")) {
				t.Fatalf("incomplete SDK sequence: %s", history)
			}
			var limits struct{ MaxTokens int }
			if err := json.Unmarshal(detail.Run.Options, &limits); err != nil {
				t.Fatal(err)
			}
			wantBudget := 7 * 300000
			if mode == "explicit-budget" {
				wantBudget = 1
			}
			if limits.MaxTokens != wantBudget {
				t.Fatalf("workflow budget lost: %+v", limits)
			}
			if len(detail.Nodes) != 1 || detail.Nodes[0].NodeID != "delivery" {
				t.Fatalf("not one SDK delivery: %+v", detail.Nodes)
			}
			afterBytes, _ := os.ReadFile(data)
			if string(afterBytes) != string(input) {
				t.Fatal("source data changed")
			}
		})
	}
}
