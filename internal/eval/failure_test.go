package eval_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"math"
	"testing"
)

type engineFunc func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (f engineFunc) Run(c context.Context, s string, j json.RawMessage) (json.RawMessage, error) {
	return f(c, s, j)
}

type judgeFunc func(context.Context, string, json.RawMessage) (bool, float64, []string, error)

func (f judgeFunc) Score(c context.Context, s string, j json.RawMessage) (bool, float64, []string, error) {
	return f(c, s, j)
}
func scoredCase(id string) store.EvalCase {
	return store.EvalCase{ID: id, Input: json.RawMessage(`{"n":1}`), Checks: []store.EvalCheckSpec{{Kind: "json-parse"}}}
}

func TestExecutionFailureCannotBecomeJSONPassOrReachJudge(t *testing.T) {
	calls, judges := 0, 0
	cases := []store.EvalCase{scoredCase("good"), scoredCase("failed"), scoredCase("never")}
	for i := range cases {
		cases[i].Criteria = "criterion"
	}
	failure := errors.New("transport disconnected")
	run, err := eval.RunInMemory(t.Context(), store.Evalset{ID: "errors", Cases: cases}, eval.Config{Target: "instructions", Engine: engineFunc(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		if calls == 2 {
			return json.RawMessage(`null`), failure
		}
		return json.RawMessage(`{"ok":true}`), nil
	}), Judge: judgeFunc(func(context.Context, string, json.RawMessage) (bool, float64, []string, error) {
		judges++
		return true, .9, []string{"reason"}, nil
	})})
	if !errors.Is(err, failure) || run.Status != "failed" || run.PassRate != 0 || run.MeanScore != 0 || calls != 2 || judges != 1 || len(run.Results) != 2 || run.Results[1].Passed || run.Results[1].Score != 0 || !run.Results[0].Passed {
		t.Fatalf("failure scored as quality: %+v err=%v calls=%d judges=%d", run, err, calls, judges)
	}
	if run.Results[1].Case == nil || run.Results[1].Case.Criteria != "criterion" {
		t.Fatal("lost frozen failed case")
	}
}
func TestJudgeFailureOrInvalidScoreCannotCarrySuccess(t *testing.T) {
	for _, score := range []float64{.99, math.NaN(), math.Inf(1), -1, 1.1} {
		t.Run("invalid", func(t *testing.T) {
			c := scoredCase("case")
			c.Criteria = "criterion"
			run, err := eval.RunInMemory(t.Context(), store.Evalset{ID: "judge-errors", Cases: []store.EvalCase{c}}, eval.Config{Target: "instructions", Engine: &eval.MockEngine{}, Judge: judgeFunc(func(context.Context, string, json.RawMessage) (bool, float64, []string, error) {
				if score == .99 {
					return true, score, nil, errors.New("judge transport failure")
				}
				return true, score, nil, nil
			})})
			if err == nil || run.Status != "failed" || run.PassRate != 0 || run.MeanScore != 0 || run.Results[0].Passed || run.Results[0].Score != 0 {
				t.Fatalf("invalid judge passed: %+v %v", run, err)
			}
		})
	}
}
func TestInvalidScoringIsRejectedBeforeEngine(t *testing.T) {
	negative := -1
	for _, cases := range [][]store.EvalCase{nil, {scoredCase("x"), scoredCase("x")}, {{ID: "unscored"}}, {{ID: "unknown", Checks: []store.EvalCheckSpec{{Kind: "unknown"}}}}, {{ID: "min", Checks: []store.EvalCheckSpec{{Kind: "count-min"}}}}, {{ID: "negative", Checks: []store.EvalCheckSpec{{Kind: "count-min", Min: &negative}}}}, {{ID: "criteria", Criteria: "unmeasured"}}} {
		calls := 0
		run, err := eval.RunInMemory(t.Context(), store.Evalset{Cases: cases}, eval.Config{Target: "instructions", Engine: engineFunc(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, nil })})
		if err == nil || calls != 0 || run.Status != "failed" {
			t.Fatalf("invalid config ran: %+v %v", run, err)
		}
	}
}
func TestEmptyEscapedTextAndNullArrayFailChecks(t *testing.T) {
	zero := 0
	for _, input := range []struct {
		output string
		check  store.EvalCheckSpec
	}{{`"\n\t"`, store.EvalCheckSpec{Kind: "text-nonempty"}}, {`null`, store.EvalCheckSpec{Kind: "count-min", Min: &zero}}} {
		run, err := eval.RunInMemory(t.Context(), store.Evalset{Cases: []store.EvalCase{{ID: "empty", Checks: []store.EvalCheckSpec{input.check}}}}, eval.Config{Target: "instructions", Engine: &eval.MockEngine{Responses: map[string]json.RawMessage{"": json.RawMessage(input.output)}}})
		if err != nil || run.Status != "done" || run.PassRate != 0 {
			t.Fatalf("empty output passed: %+v %v", run, err)
		}
	}
}
