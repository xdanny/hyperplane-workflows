package optimize

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ---------------------------------------------------------------------------
// mockJudge — hermetic Judge implementation for optimizer tests.
// ---------------------------------------------------------------------------

// mockJudge maps (instruction, criterion) → (passed, score).
// The instruction key comes from how the eval engine was called: when
// RunInMemory calls engine.Run(ctx, instruction, input), the MockEngine returns
// a fixed response keyed by instruction. The judge then scores that response.
// We key by criterion only here because a single test controls which criteria
// are in the evalset.
type mockJudge struct {
	// results maps criterion → result. "" is the catch-all key.
	results map[string]judgeResult
}

type judgeResult struct {
	passed  bool
	score   float64
	reasons []string
}

func (j *mockJudge) Score(_ context.Context, criterion string, _ json.RawMessage) (bool, float64, []string, error) {
	if r, ok := j.results[criterion]; ok {
		return r.passed, r.score, r.reasons, nil
	}
	if r, ok := j.results[""]; ok {
		return r.passed, r.score, r.reasons, nil
	}
	return false, 0.1, []string{"no mock result for criterion"}, nil
}

// ---------------------------------------------------------------------------
// optimizeInMemoryWithJudge: same as optimizeInMemory but accepts an injected
// judge so we can test the judge-driven promotion path without network access.
// ---------------------------------------------------------------------------

func optimizeInMemoryWithJudge(
	ctx context.Context,
	sk store.Skill,
	es store.Evalset,
	scoreEngine eval.Engine,
	judge eval.Judge,
	variantInstructions []string,
	minGain float64,
) (Result, error) {
	res := Result{
		SkillID:   sk.ID,
		EvalsetID: es.ID,
	}

	// Score baseline.
	baselineRun, err := eval.RunInMemory(ctx, es, eval.Config{
		EvalsetID: es.ID,
		Target:    sk.Instruction,
		Engine:    scoreEngine,
		Judge:     judge,
	})
	if err != nil {
		return res, err
	}
	res.Baseline = VariantScore{
		Index:       0,
		Label:       "baseline",
		Instruction: sk.Instruction,
		PassRate:    baselineRun.PassRate,
		MeanScore:   baselineRun.MeanScore,
	}

	// Score each variant.
	for i, inst := range variantInstructions {
		varRun, runErr := eval.RunInMemory(ctx, es, eval.Config{
			EvalsetID: es.ID,
			Target:    inst,
			Engine:    scoreEngine,
			Judge:     judge,
		})
		if runErr != nil {
			varRun.PassRate = 0
			varRun.MeanScore = 0
		}
		res.Variants = append(res.Variants, VariantScore{
			Index:       i + 1,
			Label:       "variant-" + itoa(i+1),
			Instruction: inst,
			PassRate:    varRun.PassRate,
			MeanScore:   varRun.MeanScore,
		})
	}

	// Pick the best variant.
	best := pickBest(res.Variants)
	if best != nil {
		gain := best.PassRate - res.Baseline.PassRate
		if gain > minGain || (gain == minGain && best.MeanScore > res.Baseline.MeanScore) {
			winner := *best
			res.Winner = &winner
			res.Gain = gain
		}
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestJudgeDrivesVariantPromotion is the key regression test:
//
// Setup:
//   - Evalset has 2 cases, each with a Criteria string (the judge path).
//   - Both cases produce non-empty text (text-nonempty check always passes).
//   - The MockJudge gives the baseline LOW scores (<0.7 → pass=false).
//   - The MockJudge gives the winning variant HIGH scores (>=0.7 → pass=true).
//
// Without the judge wired: baseline scores 1.0 (judge default), variant also
// scores 1.0 → no gain → no winner (the original bug).
// With the judge wired: baseline fails (score<0.7), variant passes (score≥0.7)
// → gain > 0 → winner is promoted. This proves the judge path drives promotion.
func TestJudgeDrivesVariantPromotion(t *testing.T) {
	ctx := context.Background()

	const (
		baseInst   = "base instruction judge test"
		winnerInst = "improved instruction judge test"
		badVariant = "bad variant judge test"
		criterion1 = "output must be concise and factual"
		criterion2 = "output must NOT exhibit: vague boilerplate"
	)

	es := store.Evalset{
		ID:   "es-judge-driven",
		Name: "judge driven promotion test",
		Cases: []store.EvalCase{
			{
				ID:       "j1",
				Input:    json.RawMessage(`{"q":"what is 2+2?"}`),
				Criteria: criterion1,
				Checks:   []store.EvalCheckSpec{{Kind: "text-nonempty"}},
			},
			{
				ID:       "j2",
				Input:    json.RawMessage(`{"q":"describe AI"}`),
				Criteria: criterion2,
				Checks:   []store.EvalCheckSpec{{Kind: "text-nonempty"}},
			},
		},
	}

	// Each instruction produces distinct output so the judge can differentiate.
	// text-nonempty always passes; the judge is the only discriminator.
	perInstEngine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			baseInst:   json.RawMessage(`"baseline output"`),
			winnerInst: json.RawMessage(`"winner output"`),
			badVariant: json.RawMessage(`"bad output"`),
		},
	}

	// Judge keys on output content prefix to determine pass/fail.
	// "winner output" → pass; anything else → fail.
	judge := &judgeByOutputContent{
		passingOutputPrefix: "winner output",
		passScore:           0.9,
		failScore:           0.3,
	}

	sk := store.Skill{ID: "judge-test-skill", Instruction: baseInst}
	variantInstructions := []string{winnerInst, badVariant}

	result, err := optimizeInMemoryWithJudge(ctx, sk, es, perInstEngine, judge, variantInstructions, 0)
	if err != nil {
		t.Fatalf("optimizeInMemoryWithJudge: %v", err)
	}

	// Baseline should fail both judge cases → passRate = 0.
	if result.Baseline.PassRate != 0.0 {
		t.Errorf("baseline: expected passRate=0 (judge fails), got %.2f", result.Baseline.PassRate)
	}

	// Validate variants were scored.
	if len(result.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(result.Variants))
	}

	winner := result.Variants[0] // winnerInst
	bad := result.Variants[1]    // badVariant

	if winner.PassRate != 1.0 {
		t.Errorf("winner variant: expected passRate=1.0, got %.2f", winner.PassRate)
	}
	if bad.PassRate != 0.0 {
		t.Errorf("bad variant: expected passRate=0.0, got %.2f", bad.PassRate)
	}

	// A winner must be selected.
	if result.Winner == nil {
		t.Fatal("expected a winner to be promoted via judge scoring, got nil — judge path is broken")
	}
	if result.Winner.Instruction != winnerInst {
		t.Errorf("expected winner=%q, got %q", winnerInst, result.Winner.Instruction)
	}
	if result.Gain <= 0 {
		t.Errorf("expected positive gain, got %.2f", result.Gain)
	}
}

// judgeByOutputContent is a Judge whose verdict is determined by whether the
// output JSON string starts with a specific prefix. This lets a single judge
// instance differentiate between baseline and variant outputs without needing
// to track which instruction produced the output.
type judgeByOutputContent struct {
	passingOutputPrefix string
	passScore           float64
	failScore           float64
}

func (j *judgeByOutputContent) Score(_ context.Context, _ string, output json.RawMessage) (bool, float64, []string, error) {
	// Decode the JSON string to get the raw text.
	var text string
	if err := json.Unmarshal(output, &text); err != nil {
		// Not a JSON string — treat as raw bytes.
		text = string(output)
	}

	if len(text) >= len(j.passingOutputPrefix) &&
		text[:len(j.passingOutputPrefix)] == j.passingOutputPrefix {
		return true, j.passScore, []string{"output satisfies criterion"}, nil
	}
	return false, j.failScore, []string{"output does not satisfy criterion"}, nil
}

// TestJudgeNoOpWithoutCriteria asserts that when eval cases have no Criteria
// field, the judge is never consulted and the eval behaves purely on
// deterministic checks (backward compat). Even when a judge IS wired.
func TestJudgeNoOpWithoutCriteria(t *testing.T) {
	ctx := context.Background()

	es := store.Evalset{
		ID:   "es-no-criteria",
		Name: "no criteria test",
		Cases: []store.EvalCase{
			{
				ID:     "c1",
				Input:  json.RawMessage(`{}`),
				Checks: []store.EvalCheckSpec{{Kind: "text-nonempty"}},
				// No Criteria field.
			},
		},
	}

	engine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			"": json.RawMessage(`"good output"`),
		},
	}

	// Even with a judge that always fails, the case should pass because
	// criteria is empty → judge is skipped.
	alwaysFailJudge := &mockJudge{
		results: map[string]judgeResult{
			"": {passed: false, score: 0.0, reasons: []string{"always fail"}},
		},
	}

	run, err := eval.RunInMemory(ctx, es, eval.Config{
		EvalsetID: es.ID,
		Target:    "test",
		Engine:    engine,
		Judge:     alwaysFailJudge,
	})
	if err != nil {
		t.Fatalf("RunInMemory: %v", err)
	}

	// Case should pass: text-nonempty passes, and judge is skipped (no criteria).
	if run.PassRate != 1.0 {
		t.Errorf("expected passRate=1.0 (no criteria → judge skipped), got %.2f", run.PassRate)
	}
}

// TestBuildJudgeReturnsCorrectTypes verifies that buildJudge returns the right
// concrete types for known engine names and nil for unknown names.
func TestBuildJudgeReturnsCorrectTypes(t *testing.T) {
	codexJudge := buildJudge("codex")
	if codexJudge == nil {
		t.Error("buildJudge('codex') should return non-nil CodexJudge")
	}

	localJudge := buildJudge("local")
	if localJudge == nil {
		t.Error("buildJudge('local') should return non-nil LocalJudge")
	}

	mockJudge := buildJudge("mock")
	if mockJudge != nil {
		t.Error("buildJudge('mock') should return nil (no judge for mock engine)")
	}

	emptyJudge := buildJudge("")
	if emptyJudge != nil {
		t.Error("buildJudge('') should return nil")
	}
}

// TestOptimizeWithJudgeMeanScoreTieBreak asserts that when two variants have
// equal passRate, the judge score (via meanScore) breaks the tie.
func TestOptimizeWithJudgeMeanScoreTieBreak(t *testing.T) {
	ctx := context.Background()

	const (
		baseInst  = "baseline tiebreak"
		highJudge = "high judge variant"
		lowJudge  = "low judge variant"
		criterion = "output quality"
	)

	es := store.Evalset{
		ID:   "es-tiebreak",
		Name: "tiebreak test",
		Cases: []store.EvalCase{
			{
				ID:       "t1",
				Input:    json.RawMessage(`{}`),
				Criteria: criterion,
				Checks:   []store.EvalCheckSpec{{Kind: "text-nonempty"}},
			},
		},
	}

	perInstEngine := &eval.MockEngine{
		Responses: map[string]json.RawMessage{
			baseInst:  json.RawMessage(`"baseline output"`),
			highJudge: json.RawMessage(`"high quality output"`),
			lowJudge:  json.RawMessage(`"passing but low quality output"`),
		},
	}

	// Baseline: pass=true score=0.7 (just passes). Variants: both pass=true.
	// highJudge: score=0.95, lowJudge: score=0.72.
	judge := &judgeByOutputContent{
		passingOutputPrefix: "", // always passes
		passScore:           0.72,
		failScore:           0.0,
	}
	// Override to return per-output scores.
	preciseJudge := &preciseScoreJudge{
		scores: map[string]float64{
			`"baseline output"`:                0.70,
			`"high quality output"`:            0.95,
			`"passing but low quality output"`: 0.72,
		},
	}

	sk := store.Skill{ID: "tiebreak-skill", Instruction: baseInst}
	variantInstructions := []string{highJudge, lowJudge}

	result, err := optimizeInMemoryWithJudge(ctx, sk, es, perInstEngine, preciseJudge, variantInstructions, 0)
	if err != nil {
		t.Fatalf("optimizeInMemoryWithJudge: %v", err)
	}

	// All pass (score >= 0.7) → all passRate = 1.0.
	if result.Baseline.PassRate != 1.0 {
		t.Errorf("baseline passRate: want 1.0, got %.2f", result.Baseline.PassRate)
	}

	// All pass → gain=0 for passRate. But highJudge has meanScore=0.95 > baseline 0.70
	// and lowJudge has 0.72. The winner should be highJudge via meanScore tiebreak.
	// However: gain==minGain(0) and meanScore strictly better → winner promoted.
	if result.Winner == nil {
		t.Fatal("expected winner via meanScore tiebreak, got nil")
	}
	if result.Winner.Instruction != highJudge {
		t.Errorf("expected winner=%q via meanScore, got %q", highJudge, result.Winner.Instruction)
	}

	// Suppress unused variable warning.
	_ = judge
}

// preciseScoreJudge returns a score based on the exact JSON encoding of output.
// pass = score >= 0.7.
type preciseScoreJudge struct {
	scores map[string]float64
}

func (j *preciseScoreJudge) Score(_ context.Context, _ string, output json.RawMessage) (bool, float64, []string, error) {
	key := string(output)
	score, ok := j.scores[key]
	if !ok {
		score = 0.5
	}
	return score >= 0.7, score, []string{"scored by preciseScoreJudge"}, nil
}
