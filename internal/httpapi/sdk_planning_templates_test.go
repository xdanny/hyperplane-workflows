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

// Exercises the shipped templates through refresh, preparation, save and real
// SDK transport. The executable returns fixtures, not real research or reviews.
func TestSDKPlanningTemplates(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/planning-template"))
	for _, candidate := range seed.DefaultTemplates {
		if candidate.Name != "trip-planning" && candidate.Name != "deep-research" && candidate.Name != "interview-prep" && candidate.Name != "code-review" && candidate.Name != "finance-review" {
			continue
		}
		modes := []string{"pass", "reject"}
		if candidate.Name == "code-review" {
			modes = append(modes, "human-reject")
		}
		for _, mode := range modes {
			t.Run(candidate.Name+"/"+mode, func(t *testing.T) {
				t.Setenv("SDK_FIXTURE_MODE", mode)
				srv, ts := newTestServer(t, nil)
				ctx := context.Background()
				st, err := store.Open(ctx, os.Getenv("DATABASE_URL")) // TestMain supplies its disposable database.
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(st.Close)
				template := candidate
				template.Name += "-sdk-planning-" + mode
				// Simulate make seed updating an existing graph template in place.
				legacy, err := st.CreateTemplate(ctx, store.WorkflowTemplate{Name: template.Name, Spec: store.JSON(`{"name":"old-graph","nodes":[]}`)})
				if err != nil {
					t.Fatal(err)
				}
				if err := st.RefreshTemplateByName(ctx, template); err != nil {
					t.Fatal(err)
				}
				endpoint := ts.URL + "/api/templates/" + legacy.ID + "/instantiate"
				beforeDrafts := storedDraftSnapshot(t)
				values := map[string]string{}
				for _, slot := range candidate.ContextSlots {
					values[slot.ID] = "Provided " + slot.ID + ": quoted \"fact\"\nSecond line"
				}
				for _, slot := range candidate.ContextSlots {
					if slot.Required {
						original := values[slot.ID]
						delete(values, slot.ID)
						status, raw := doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": values})
						if status != 400 || !strings.Contains(string(raw), slot.ID) {
							t.Fatalf("missing %s accepted: %d %s", slot.ID, status, raw)
						}
						values[slot.ID] = original
					}
				}
				calls := filepath.Join(t.TempDir(), "calls.jsonl")
				t.Setenv("SDK_FIXTURE_CALLS", calls)
				status, raw := doJSON(t, http.MethodPost, endpoint, map[string]any{"slots": values, "project": "sdk-planning"})
				var prepared struct {
					Workflow store.SDKWorkflow `json:"workflow"`
					Draft    json.RawMessage   `json:"draft"`
				}
				if status != 201 || json.Unmarshal(raw, &prepared) != nil || prepared.Workflow.Kind != "nodes" || len(prepared.Draft) != 0 {
					t.Fatalf("prepare: %d %s", status, raw)
				}
				if _, err := os.Stat(calls); !os.IsNotExist(err) {
					t.Fatal("preparation invoked a model")
				}
				afterDrafts := storedDraftSnapshot(t)
				if string(beforeDrafts) != string(afterDrafts) {
					t.Fatal("preparation created a graph draft")
				}
				// Preparation carries the template's questions instead of writing
				// this run's answers into the prose, so the saved result can run
				// again next week with new answers.
				if len(prepared.Workflow.Inputs) != len(candidate.ContextSlots) {
					t.Fatalf("prepared workflow lost the template's inputs: %+v", prepared.Workflow.Inputs)
				}
				for _, value := range values {
					if strings.Contains(string(raw), value) {
						t.Fatal("preparation baked this run's answers into the steps")
					}
				}
				if prepared.Workflow.Project != "sdk-planning" {
					t.Fatal("lost project")
				}
				status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/workflows", prepared.Workflow)
				var saved store.SDKWorkflow
				if status != 201 || json.Unmarshal(raw, &saved) != nil || saved.ID == "" {
					t.Fatalf("save: %d %s", status, raw)
				}
				// The fixture checks the prompts against the definition the run
				// actually executes: the saved steps with this run's answers in
				// them, which is what launching a saved workflow produces.
				filled, accepted, err := store.FillWorkflowSlots(saved, values)
				if err != nil {
					t.Fatal(err)
				}
				definition, err := json.Marshal(filled)
				if err != nil {
					t.Fatal(err)
				}
				var prose strings.Builder
				prose.WriteString(filled.Instruction)
				for _, node := range filled.Nodes {
					prose.WriteString("\n" + node.Instruction)
					for _, value := range node.Inputs {
						prose.WriteString("\n" + value)
					}
				}
				for _, slot := range candidate.ContextSlots {
					if value := accepted[slot.ID]; value != "" && !strings.Contains(prose.String(), value) {
						t.Fatalf("input %s lost or changed on the way into the run", slot.ID)
					}
				}
				if strings.Contains(prose.String(), "{slot:") {
					t.Fatal("unbound context reached the run")
				}
				definitionPath := filepath.Join(t.TempDir(), "definition.json")
				if err := os.WriteFile(definitionPath, definition, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("SDK_FIXTURE_DEFINITION", definitionPath)
				status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/run", map[string]any{"sourceWorkflowId": saved.ID, "revision": saved.Revision, "slots": values, "project": saved.Project})
				var launch struct{ RunID string }
				if status != 202 || json.Unmarshal(raw, &launch) != nil || launch.RunID == "" {
					t.Fatalf("launch: %d %s", status, raw)
				}
				var detail struct {
					Run   store.Run
					Nodes []store.Node
				}
				deadline := time.Now().Add(12 * time.Second)
				decided := false
				for time.Now().Before(deadline) {
					_, raw = doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+launch.RunID, nil)
					if err := json.Unmarshal(raw, &detail); err != nil {
						t.Fatal(err)
					}
					var snapshot struct {
						SDK struct {
							Phase    string
							Approval *struct{ ID string }
						}
					}
					if err := json.Unmarshal(detail.Run.Doc, &snapshot); err != nil {
						t.Fatal(err)
					}
					if !decided && snapshot.SDK.Phase == "approval_pending" && snapshot.SDK.Approval != nil {
						decision := "approved"
						if mode == "human-reject" {
							decision = "rejected"
						}
						status, raw = doJSON(t, http.MethodPost, ts.URL+"/api/approvals/"+snapshot.SDK.Approval.ID+"/decision", map[string]string{"decision": decision})
						if status != 200 {
							t.Fatalf("fixture decision: %d %s", status, raw)
						}
						decided = true
					}
					if detail.Run.State != "running" {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				wantState, wantCalls := "passed", len(saved.Nodes)
				for _, node := range saved.Nodes {
					if node.Kind == "approval" {
						wantCalls--
					}
				}
				if mode == "human-reject" {
					wantState = "failed"
				}
				if mode == "reject" {
					wantState = "failed"
					for i, node := range saved.Nodes {
						if node.Kind == "check" {
							wantCalls = i + 1
							break
						}
					}
				}
				if !srv.DrainRuns(5*time.Second) || detail.Run.State != wantState || len(detail.Nodes) != 1 || detail.Nodes[0].Kind != "sdk" {
					t.Fatalf("SDK lifecycle: %s %s", detail.Run.State, detail.Run.Doc)
				}
				if candidate.Name == "code-review" && mode != "reject" && !decided {
					t.Fatal("code review completed without the required decision")
				}
				callData, err := os.ReadFile(calls)
				if err != nil || len(strings.Split(strings.TrimSpace(string(callData)), "\n")) != wantCalls {
					t.Fatalf("calls: %s %v", callData, err)
				}
				if !strings.Contains(string(detail.Run.Doc), "Fixture-only verdict") {
					t.Fatal("review caveat lost")
				}
			})
		}
	}
}
