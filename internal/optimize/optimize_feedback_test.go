package optimize

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestOptimizeFeedbackIsOnlyProposalContext(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "optimize-feedback-" + time.Now().Format("150405.000000000")
	source := id + "-source"
	createFeedbackSkill(t, st, id, []string{source})
	if err := st.CreateRun(ctx, feedbackRun(source, "passed", "SOURCE CAVEAT do not invent missing metadata")); err != nil {
		t.Fatal(err)
	}
	es := store.Evalset{ID: id + "-eval", Name: "Configured cases", SkillRef: id, Cases: []store.EvalCase{{ID: "real-case", Input: store.JSON(`{"task":"CONFIGURED INPUT"}`), Criteria: "Explain concrete limits", Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}}}}
	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var prompts []string
	var failVariant atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var parts []string
		for _, m := range body.Messages {
			parts = append(parts, m.Content)
		}
		prompt := strings.Join(parts, "\n")
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
		if failVariant.Load() && strings.Contains(prompt, "Improved instruction\n\nINPUT:") {
			http.Error(w, "controlled scoring outage", http.StatusServiceUnavailable)
			return
		}
		answer := "Concrete explanation"
		if strings.Contains(prompt, "You are improving a skill instruction") {
			answer = `["Improved instruction"]`
		} else if strings.Contains(prompt, "CRITERION:") {
			answer = `{"score":0.9,"pass":true,"reasons":["limited scope"]}`
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", server.URL+"/v1")
	t.Setenv("LOCAL_LLM_API_KEY", "")
	result, err := Optimize(ctx, st, Config{SkillID: id, FromRuns: true, Variants: 1, Proposer: "local", ScoreEngine: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.FeedbackGrounded || result.HarvestSummary.SDK != 1 {
		t.Fatalf("missing feedback metadata: %+v", result)
	}
	if len(prompts) != 5 {
		t.Fatalf("feedback changed configured evaluation case count: %d", len(prompts))
	}
	for _, p := range prompts {
		proposal := strings.Contains(p, "You are improving a skill instruction")
		if strings.Contains(p, "SOURCE CAVEAT") != proposal {
			t.Fatalf("feedback routed outside proposal: %s", p)
		}
	}
	stored, err := st.GetEvalset(ctx, es.ID)
	if err != nil || len(stored.Cases) != 1 || stored.Cases[0].ID != "real-case" {
		t.Fatalf("configured evalset changed: %+v %v", stored, err)
	}
	beforeRuns, err := ListOptimizeRuns(ctx, st, id)
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls := len(prompts)
	failVariant.Store(true)
	failed, err := Optimize(ctx, st, Config{SkillID: id, FromRuns: true, Variants: 1, Proposer: "local", ScoreEngine: "local", Apply: true})
	if err == nil || !strings.Contains(err.Error(), "score variant 1") || failed.Applied {
		t.Fatalf("scoring failure treated as completed optimize: %+v %v", failed, err)
	}
	if len(prompts)-beforeCalls != 4 {
		t.Fatalf("failed variant was judged or extra calls made: %d", len(prompts)-beforeCalls)
	}
	afterRuns, err := ListOptimizeRuns(ctx, st, id)
	if err != nil || len(afterRuns) != len(beforeRuns) {
		t.Fatalf("failed optimization recorded as scored: %+v %v", afterRuns, err)
	}
	unchanged, err := st.GetSkill(ctx, id)
	if err != nil || unchanged.Instruction != "Write useful explanations" {
		t.Fatalf("failed score rewrote skill: %+v %v", unchanged, err)
	}
	createFeedbackSkill(t, st, id, nil)
	before := len(prompts)
	_, err = Optimize(ctx, st, Config{SkillID: id, FromRuns: true, Proposer: "local", ScoreEngine: "local"})
	if !errors.Is(err, ErrNoFeedback) || len(prompts) != before {
		t.Fatalf("no-feedback made model calls: %v %d/%d", err, len(prompts), before)
	}
}
