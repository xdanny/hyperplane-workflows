package eval_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ---------------------------------------------------------------------------
// MockJudge — injected in place of Codex/Local for hermetic testing.
// ---------------------------------------------------------------------------

// MockJudge is a deterministic Judge whose Score result is controlled by
// mapping criterion → (passed, score, reasons). If no key matches, it uses
// the empty-string key as a catch-all.
type MockJudge struct {
	// Results maps criterion → scoreResult.
	Results map[string]mockJudgeResult
}

type mockJudgeResult struct {
	Passed  bool
	Score   float64
	Reasons []string
}

func (j *MockJudge) Score(_ context.Context, criterion string, _ json.RawMessage) (passed bool, score float64, reasons []string, err error) {
	if r, ok := j.Results[criterion]; ok {
		return r.Passed, r.Score, r.Reasons, nil
	}
	if r, ok := j.Results[""]; ok {
		return r.Passed, r.Score, r.Reasons, nil
	}
	// Default: fail with low score.
	return false, 0.2, []string{"no mock result configured for criterion"}, nil
}

// ---------------------------------------------------------------------------
// parseJudgeResponse tests (via the exported path through RunInMemory)
// ---------------------------------------------------------------------------

// TestJudgePassesGoodOutput asserts that when the judge says score=0.9/pass=true
// the eval case is marked passed and carries the judge score.
func TestJudgePassesGoodOutput(t *testing.T) {
	ctx := context.Background()

	es := store.Evalset{
		ID:   "judge-good",
		Name: "judge good output test",
		Cases: []store.EvalCase{
			{
				ID:       "c1",
				Input:    json.RawMessage(`{}`),
				Criteria: "the output should be a clear greeting",
				Checks:   []store.EvalCheckSpec{{Kind: "text-nonempty"}},
			},
		},
	}

	engine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			"": json.RawMessage(`"Hello, world!"`),
		},
	}

	judge := &MockJudge{
		Results: map[string]mockJudgeResult{
			"the output should be a clear greeting": {
				Passed:  true,
				Score:   0.9,
				Reasons: []string{"output is a clear, friendly greeting"},
			},
		},
	}

	run, err := eval.RunInMemory(ctx, es, eval.Config{
		EvalsetID: es.ID,
		Target:    "say hello",
		Engine:    engine,
		Judge:     judge,
	})
	if err != nil {
		t.Fatalf("RunInMemory: %v", err)
	}

	if run.PassRate != 1.0 {
		t.Errorf("expected passRate=1.0, got %.2f", run.PassRate)
	}
	if len(run.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(run.Results))
	}
	r := run.Results[0]
	if !r.Passed {
		t.Errorf("expected case to pass, got failed; reasons: %v", r.Reasons)
	}
	// Score should reflect the judge score (0.9), not the default 1.0.
	if r.Score < 0.8 || r.Score > 1.0 {
		t.Errorf("expected score near 0.9, got %.2f", r.Score)
	}
}

// TestJudgeFailsBadOutput asserts that when the judge gives score=0.3/pass=false
// the case is marked failed even if text-nonempty passes.
func TestJudgeFailsBadOutput(t *testing.T) {
	ctx := context.Background()

	es := store.Evalset{
		ID:   "judge-bad",
		Name: "judge bad output test",
		Cases: []store.EvalCase{
			{
				ID:       "c1",
				Input:    json.RawMessage(`{}`),
				Criteria: "must NOT exhibit: vague boilerplate",
				Checks:   []store.EvalCheckSpec{{Kind: "text-nonempty"}},
			},
		},
	}

	engine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			// Non-empty output — text-nonempty check passes — but the judge should veto it.
			"": json.RawMessage(`"As an AI language model, I can help you with that."`),
		},
	}

	judge := &MockJudge{
		Results: map[string]mockJudgeResult{
			"must NOT exhibit: vague boilerplate": {
				Passed:  false,
				Score:   0.3,
				Reasons: []string{"output is vague boilerplate", "fails criterion"},
			},
		},
	}

	run, err := eval.RunInMemory(ctx, es, eval.Config{
		EvalsetID: es.ID,
		Target:    "answer the question",
		Engine:    engine,
		Judge:     judge,
	})
	if err != nil {
		t.Fatalf("RunInMemory: %v", err)
	}

	if run.PassRate != 0.0 {
		t.Errorf("expected passRate=0.0 (judge vetoed), got %.2f", run.PassRate)
	}
	r := run.Results[0]
	if r.Passed {
		t.Errorf("expected case to fail (judge veto), but it passed")
	}
	// Score is the judgeScore (0.3) because checkPassed=true; score is only zeroed
	// when the deterministic check fails (not the judge). The case is marked failed
	// because judgePassed=false, which is what matters for passRate.
	if r.Score > 0.5 {
		t.Errorf("expected low score (judge fail) but got %.2f", r.Score)
	}
}

// A missing judge cannot silently certify unmeasured criteria.
func TestJudgeNilRejectsCriteria(t *testing.T) {
	ctx := context.Background()

	es := store.Evalset{
		ID:   "judge-nil",
		Name: "nil judge test",
		Cases: []store.EvalCase{
			{
				ID:       "c1",
				Input:    json.RawMessage(`{}`),
				Criteria: "some unmeasured criterion",
				Checks:   []store.EvalCheckSpec{{Kind: "text-nonempty"}},
			},
		},
	}

	engine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			"": json.RawMessage(`"non-empty output"`),
		},
	}

	run, err := eval.RunInMemory(ctx, es, eval.Config{
		EvalsetID: es.ID,
		Target:    "do something",
		Engine:    engine,
		Judge:     nil, // no judge wired
	})
	if err == nil || run.Status != "failed" || run.PassRate != 0 || run.MeanScore != 0 || len(run.Results) != 0 {
		t.Fatalf("unmeasured criteria passed: %+v %v", run, err)
	}
}
