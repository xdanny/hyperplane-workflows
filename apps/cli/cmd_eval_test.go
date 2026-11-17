package main

import (
	"encoding/json"
	"fmt"
	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEvalCLIUsesSkillReferenceAndRefusesChangedComparison(t *testing.T) {
	st := knowledgeCLIStore(t)
	ctx := t.Context()
	id := fmt.Sprintf("cli-eval-%d", time.Now().UnixNano())
	sk := store.Skill{ID: id + "-skill", Name: id, Instruction: "Actual skill instructions"}
	if err := st.UpsertSkill(ctx, sk); err != nil {
		t.Fatal(err)
	}
	es := store.Evalset{ID: id, Name: id, SkillRef: sk.ID, Cases: []store.EvalCase{{ID: "one", Input: json.RawMessage(`{"n":1}`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}}}}
	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatal(err)
	}
	run := func() store.EvalRun {
		code, output := knowledgeCLIOutput(t, func() int { return evalRun(ctx, st, []string{id, "--mock", "--json"}) })
		var receipt store.EvalRun
		if code != 0 || json.Unmarshal(output, &receipt) != nil || receipt.Engine != "mock" || receipt.Target != sk.Instruction || receipt.Results[0].Case == nil {
			t.Fatalf("CLI lost resolved target: exit=%d output=%s", code, output)
		}
		return receipt
	}
	first := run()
	es.Cases[0].Input = json.RawMessage(`{"n":2}`)
	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatal(err)
	}
	second := run()
	if code, _ := knowledgeCLIOutput(t, func() int { return evalCompare(ctx, st, []string{first.ID, second.ID, "--json"}) }); code == 0 {
		t.Fatal("CLI compared changed case snapshots")
	}
	es.Cases[0].Criteria = "Must be judged"
	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatal(err)
	}
	if code, _ := knowledgeCLIOutput(t, func() int { return evalRun(ctx, st, []string{id, "--mock", "--json"}) }); code == 0 {
		t.Fatal("mock run silently passed unjudged criteria")
	}
	if code, _ := knowledgeCLIOutput(t, func() int { return evalRun(ctx, st, []string{id, "--engine", "typo"}) }); code == 0 {
		t.Fatal("unknown engine silently invoked Codex")
	}
}

func TestRouteCLICommitsActualSDKComparisonReceipts(t *testing.T) {
	st := knowledgeCLIStore(t)
	ctx := t.Context()
	id := fmt.Sprintf("cli-route-%d", time.Now().UnixNano())
	sk := store.Skill{ID: id, Name: id, Instruction: "Actual routing instructions"}
	if err := st.UpsertSkill(ctx, sk); err != nil {
		t.Fatal(err)
	}
	es := store.Evalset{ID: id, Name: id, Cases: []store.EvalCase{{ID: "one", Input: json.RawMessage(`{"n":1}`), Checks: []store.EvalCheckSpec{{Kind: "json-parse"}}}}}
	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatal(err)
	}
	executable, err := filepath.Abs("../../internal/httpapi/testdata/routing-eval-sdk")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", executable)
	t.Setenv("SDK_FIXTURE_ROUTING_CALLS", filepath.Join(t.TempDir(), "calls.jsonl"))
	t.Setenv("SDK_FIXTURE_ROUTING_MODE", "ordinary")
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"fixture","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"{\"items\":[\"measured\"]}"}}]}`))
	}))
	defer local.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", local.URL+"/v1")
	t.Setenv("LOCAL_LLM_API_KEY", "")
	code, output := knowledgeCLIOutput(t, func() int {
		return routeEval(ctx, st, []string{"--skill", id, "--evalset", id, "--json"}, config.Config{TargetRoot: t.TempDir()})
	})
	var result eval.RoutingEvaluation
	if code != 0 || json.Unmarshal(output, &result) != nil {
		t.Fatalf("route CLI failed: %d %s", code, output)
	}
	policy, err := st.GetRoutingPolicy(ctx, "skill:"+id)
	if err != nil || policy.EvalRunA != result.Policy.EvalRunA || policy.EvalRunB != result.Policy.EvalRunB {
		t.Fatal("CLI policy has no matching receipt ids")
	}
	for _, runID := range []string{policy.EvalRunA, policy.EvalRunB} {
		receipt, err := st.GetEvalRun(ctx, runID)
		if err != nil || receipt.Target != sk.Instruction || receipt.Status != "done" {
			t.Fatalf("CLI receipt missing: %+v %v", receipt, err)
		}
	}
}

func TestEvalCLIComparisonRequiresSameIdentifiedJudge(t *testing.T) {
	st := knowledgeCLIStore(t)
	ctx := t.Context()
	id := fmt.Sprintf("cli-judge-identity-%d", time.Now().UnixNano())
	es := store.Evalset{ID: id, Name: id, Cases: []store.EvalCase{{ID: "one", Input: json.RawMessage(`{"n":1}`), Criteria: "Return a measured answer.", Checks: []store.EvalCheckSpec{{Kind: "json-parse"}}}}}
	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatal(err)
	}
	var requested []string
	var requestedMu sync.Mutex
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requestedMu.Lock()
		requested = append(requested, body.Model)
		requestedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": `{"score":0.9,"pass":true,"reasons":["Fixture rubric judgment."]}`}}}})
	}))
	defer local.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", local.URL+"/v1")
	t.Setenv("LOCAL_LLM_API_KEY", "")
	executable, err := filepath.Abs("../../internal/httpapi/testdata/routing-eval-sdk")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", executable)
	t.Setenv("SDK_FIXTURE_ROUTING_CALLS", filepath.Join(t.TempDir(), "calls.jsonl"))
	t.Setenv("SDK_FIXTURE_ROUTING_MODE", "ordinary")
	run := func(provider, model, target string) store.EvalRun {
		code, output := knowledgeCLIOutput(t, func() int {
			return evalRun(ctx, st, []string{id, "--mock", "--target", target, "--judge", provider, "--judge-model", model, "--json"})
		})
		var result store.EvalRun
		if code != 0 || json.Unmarshal(output, &result) != nil {
			t.Fatalf("judged run failed: %d %s", code, output)
		}
		saved, err := st.GetEvalRun(ctx, result.ID)
		if err != nil || saved.Results[0].Judge == nil || saved.Results[0].Judge.Provider != provider || saved.Results[0].Judge.Model != model || saved.Results[0].Judge.ModelDefault {
			t.Fatalf("judge identity lost: %+v %v", saved, err)
		}
		return saved
	}
	a := run("local", "judge-a", "First candidate")
	changedModel := run("local", "judge-b", "Second candidate")
	sameJudge := run("local", "judge-a", "Different candidate instructions")
	changedProvider := run("codex", "judge-a", "Third candidate")
	requestedMu.Lock()
	captured := append([]string(nil), requested...)
	requestedMu.Unlock()
	if len(captured) != 3 || captured[0] != "judge-a" || captured[1] != "judge-b" || captured[2] != "judge-a" {
		t.Fatalf("metadata disagrees with actual SDK model: %v", captured)
	}
	legacy := a
	legacy.ID += "-legacy"
	legacy.Results = append([]store.EvalCaseResult(nil), a.Results...)
	legacy.Results[0].Judge = nil
	if err := st.SaveEvalRun(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	changedRubric := a
	changedRubric.ID += "-changed-rubric"
	changedRubric.Results = append([]store.EvalCaseResult(nil), a.Results...)
	scorer := *a.Results[0].Judge
	scorer.RubricFingerprint = strings.Repeat("a", 64)
	changedRubric.Results[0].Judge = &scorer
	if err := st.SaveEvalRun(ctx, changedRubric); err != nil {
		t.Fatal(err)
	}
	for _, b := range []store.EvalRun{changedModel, changedProvider, legacy, changedRubric} {
		if code, _ := knowledgeCLIOutput(t, func() int { return evalCompare(ctx, st, []string{a.ID, b.ID, "--json"}) }); code == 0 {
			t.Fatalf("different or unknown judge compared: %s", b.ID)
		}
	}
	if code, _ := knowledgeCLIOutput(t, func() int { return evalCompare(ctx, st, []string{a.ID, sameJudge.ID, "--json"}) }); code != 0 {
		t.Fatal("same scorer rejected different candidate instructions")
	}
}
