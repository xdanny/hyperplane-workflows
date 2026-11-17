package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoutingEvaluationUsesActualSkillAndCommitsEvidence(t *testing.T) {
	for _, mode := range []string{"ordinary", "local-quality-fail", "local-error", "engine-error", "judge-error", "quality-fail", "skill-changed", "empty", "unknown-check", "missing-skill", "invalid-margin"} {
		t.Run(mode, func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls.jsonl")
			t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/httpapi/testdata/routing-eval-sdk"))
			t.Setenv("SDK_FIXTURE_ROUTING_CALLS", calls)
			t.Setenv("SDK_FIXTURE_ROUTING_MODE", mode)
			_, api := newTestServer(t, nil)
			st, err := store.Open(t.Context(), os.Getenv("DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			id := fmt.Sprintf("routing-http-%s-%d", mode, time.Now().UnixNano())
			instruction := "Exact stored skill instructions: produce a measured answer."
			sk := store.Skill{ID: id, Name: id, Instruction: instruction}
			if mode != "missing-skill" {
				if err := st.UpsertSkill(t.Context(), sk); err != nil {
					t.Fatal(err)
				}
			}
			es := store.Evalset{ID: id, Name: id, SkillRef: id, Cases: []store.EvalCase{{ID: "case", Input: json.RawMessage(`{"prompt":"same case"}`), Criteria: "The response must contain measured evidence.", Checks: []store.EvalCheckSpec{{Kind: "json-parse"}}}}}
			if mode == "empty" {
				es.Cases = nil
			}
			if mode == "unknown-check" {
				es.Cases[0].Checks[0].Kind = "unknown"
			}
			if err := st.UpsertEvalset(t.Context(), es); err != nil {
				t.Fatal(err)
			}
			scope := "skill:" + id
			if err := st.UpsertRoutingPolicy(t.Context(), store.RoutingPolicy{Scope: scope, Engine: "codex", Note: "prior"}); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var prompts []string
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid", 400)
					return
				}
				if len(body.Messages) != 1 {
					t.Errorf("unexpected messages: %+v", body.Messages)
					http.Error(w, "invalid", 400)
					return
				}
				mu.Lock()
				prompts = append(prompts, body.Messages[0].Content)
				mu.Unlock()
				if mode == "local-error" {
					http.Error(w, "fixture unavailable", 503)
					return
				}
				if mode == "skill-changed" {
					changed := sk
					changed.Instruction = "New instruction"
					if err := st.UpsertSkill(r.Context(), changed); err != nil {
						t.Error(err)
					}
				}
				content := `{"items":["measured"]}`
				if mode == "local-quality-fail" {
					content = `{"items":["local-failed-output"]}`
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}})
			}))
			defer local.Close()
			t.Setenv("LOCAL_LLM_BASE_URL", local.URL+"/v1")
			t.Setenv("LOCAL_LLM_API_KEY", "")
			margin := .1
			if mode == "local-quality-fail" {
				margin = 1
			}
			if mode == "invalid-margin" {
				margin = -1
			}
			status, raw := doJSON(t, http.MethodPost, api.URL+"/api/routing/eval", map[string]any{"skill": id, "evalset": id, "margin": margin})
			want := http.StatusOK
			switch mode {
			case "empty", "unknown-check", "invalid-margin":
				want = 400
			case "missing-skill":
				want = 404
			case "local-error", "engine-error", "judge-error":
				want = 500
			case "quality-fail":
				want = 422
			case "skill-changed":
				want = 409
			}
			if status != want {
				t.Fatalf("status=%d want=%d: %s", status, want, raw)
			}
			policy, err := st.GetRoutingPolicy(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			runs, err := st.ListEvalRuns(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			ledger, _ := os.ReadFile(calls)
			mu.Lock()
			captured := append([]string(nil), prompts...)
			mu.Unlock()
			if mode == "ordinary" || mode == "local-quality-fail" {
				wantEngine := "codex-local"
				if mode == "local-quality-fail" {
					wantEngine = "codex"
				}
				if policy.Confidence == nil || *policy.Confidence != 1 {
					t.Fatal("confidence does not reflect selected engine")
				}
				if policy.Engine != wantEngine || policy.EvalRunA == "" || policy.EvalRunB == "" || len(runs) != 2 {
					t.Fatalf("missing evidence: %+v runs=%d", policy, len(runs))
				}
				for _, run := range runs {
					if run.Status != "done" || run.Target != instruction || len(run.Results) != 1 || run.Results[0].Case == nil || run.Results[0].Case.Criteria != es.Cases[0].Criteria || run.Results[0].Judge == nil || run.Results[0].Judge.Provider != "codex" || !run.Results[0].Judge.ModelDefault || run.Engine == "mock" {
						t.Fatalf("unfrozen/incorrect receipt: %+v", run)
					}
				}
				var enginePrompt string
				judges := 0
				for _, line := range strings.Split(strings.TrimSpace(string(ledger)), "\n") {
					var call struct{ Kind, Prompt string }
					if err := json.Unmarshal([]byte(line), &call); err != nil {
						t.Fatal(err)
					}
					if call.Kind == "engine" {
						enginePrompt = call.Prompt
					} else {
						judges++
						if !strings.Contains(call.Prompt, es.Cases[0].Criteria) {
							t.Fatal("judge criteria changed")
						}
					}
				}
				if len(captured) != 1 || enginePrompt != captured[0] || !strings.HasPrefix(enginePrompt, instruction+"\n\nINPUT:\n") || strings.Contains(enginePrompt, "skill:"+id) || judges != 2 {
					t.Fatalf("engines did not evaluate same resolved instruction/input; local=%d judges=%d", len(captured), judges)
				}
			} else {
				if policy.Note != "prior" || policy.EvalRunA != "" || policy.EvalRunB != "" {
					t.Fatalf("failure authorized routing: %+v", policy)
				}
				if mode == "empty" || mode == "unknown-check" || mode == "missing-skill" || mode == "invalid-margin" {
					if len(captured) != 0 || len(ledger) != 0 || len(runs) != 0 {
						t.Fatal("invalid request reached evaluation")
					}
				}
				if mode == "local-error" {
					if len(ledger) != 0 || len(runs) != 1 || runs[0].Status != "failed" || runs[0].PassRate != 0 {
						t.Fatal("failed engine was judged or lost diagnostics")
					}
				}
				if mode == "judge-error" {
					if len(runs) != 1 || runs[0].Status != "failed" || runs[0].PassRate != 0 {
						t.Fatal("malformed judge became a quality result")
					}
				}
			}
		})
	}
}

type failedEvalEngine struct{}

func (failedEvalEngine) Run(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`null`), fmt.Errorf("fixture transport failure")
}
func TestEvalRunPersistsFailedDiagnosticsAndResolvesSkill(t *testing.T) {
	_, _ = newTestServer(t, nil)
	st, err := store.Open(t.Context(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := fmt.Sprintf("failed-eval-%d", time.Now().UnixNano())
	sk := store.Skill{ID: id, Name: id, Instruction: "Actual instruction"}
	if err := st.UpsertSkill(t.Context(), sk); err != nil {
		t.Fatal(err)
	}
	es := store.Evalset{ID: id, Name: id, SkillRef: id, Cases: []store.EvalCase{{ID: "case", Checks: []store.EvalCheckSpec{{Kind: "json-parse"}}}}}
	if err := st.UpsertEvalset(t.Context(), es); err != nil {
		t.Fatal(err)
	}
	run, err := eval.Run(t.Context(), st, eval.Config{EvalsetID: id, Engine: failedEvalEngine{}})
	if err == nil || run.ID == "" {
		t.Fatalf("failure lost: %+v %v", run, err)
	}
	saved, err := st.GetEvalRun(t.Context(), run.ID)
	if err != nil || saved.Status != "failed" || saved.PassRate != 0 || saved.Target != sk.Instruction || saved.Results[0].Passed || saved.Results[0].Case == nil {
		t.Fatalf("failed receipt: %+v %v", saved, err)
	}
}
