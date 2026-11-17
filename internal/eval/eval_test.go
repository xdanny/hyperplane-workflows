package eval_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestRunnerMockEngine(t *testing.T) {
	ctx := context.Background()

	es := store.Evalset{
		ID:   "test-evalset",
		Name: "Test Evalset",
		Cases: []store.EvalCase{
			{ID: "c1", Input: json.RawMessage(`{"x":1}`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}},
			{ID: "c2", Input: json.RawMessage(`{"x":2}`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}},
			{ID: "c3", Input: json.RawMessage(`{"x":3}`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}},
		},
	}

	engine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			"": json.RawMessage(`"hello world"`),
		},
	}

	cfg := eval.Config{
		EvalsetID: es.ID,
		Target:    "test-target",
		Engine:    engine,
	}

	run, err := eval.RunInMemory(ctx, es, cfg)
	if err != nil {
		t.Fatalf("RunInMemory: %v", err)
	}
	if run.PassRate != 1.0 {
		t.Errorf("expected passRate=1.0, got %f", run.PassRate)
	}
	if len(run.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(run.Results))
	}
	if run.Status != "done" {
		t.Errorf("expected status=done, got %s", run.Status)
	}
}

func TestRunnerEmptyOutput(t *testing.T) {
	ctx := context.Background()

	es := store.Evalset{
		ID:   "test-empty",
		Name: "Empty Output Test",
		Cases: []store.EvalCase{
			{ID: "c1", Input: json.RawMessage(`{}`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}},
		},
	}

	engine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			"": json.RawMessage(`""`),
		},
	}

	cfg := eval.Config{
		EvalsetID: es.ID,
		Target:    "test-target",
		Engine:    engine,
	}

	run, err := eval.RunInMemory(ctx, es, cfg)
	if err != nil {
		t.Fatalf("RunInMemory: %v", err)
	}
	if run.PassRate != 0.0 {
		t.Errorf("expected passRate=0.0 for empty output, got %f", run.PassRate)
	}
}

func TestCompareRuns(t *testing.T) {
	es := store.Evalset{ID: "same", Cases: []store.EvalCase{{ID: "one", Input: json.RawMessage(`1`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}}, {ID: "two", Input: json.RawMessage(`2`), Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}}}}}
	a, err := eval.RunInMemory(t.Context(), es, eval.Config{Target: "instruction", Engine: &eval.MockEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := eval.RunInMemory(t.Context(), es, eval.Config{Target: "variant", Engine: engineFunc(func(_ context.Context, _ string, input json.RawMessage) (json.RawMessage, error) {
		if string(input) == "2" {
			return json.RawMessage(`""`), nil
		}
		return json.RawMessage(`"good"`), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	result, err := eval.CompareRuns(a, b)
	if err != nil || result.Winner != "A" || result.PassRateDiff != .5 {
		t.Fatalf("comparison: %+v %v", result, err)
	}
	for _, change := range []func(*store.EvalRun){func(r *store.EvalRun) { r.Status = "failed" }, func(r *store.EvalRun) { r.Results = r.Results[:1] }, func(r *store.EvalRun) { r.Results[0].Case = nil }, func(r *store.EvalRun) { c := *r.Results[0].Case; c.Criteria = "changed"; r.Results[0].Case = &c }} {
		invalid := b
		invalid.Results = append([]store.EvalCaseResult(nil), b.Results...)
		change(&invalid)
		if result, err := eval.CompareRuns(a, invalid); err == nil || result.Winner != "" {
			t.Fatalf("invalid comparison invented winner: %+v %v", result, err)
		}
	}
}
