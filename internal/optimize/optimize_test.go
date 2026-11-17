package optimize

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// mockProposerEngine is a deterministic MockEngine that maps instruction text
// to a pre-defined response (used as the scoring engine in tests).
// It maps instruction strings to canned JSON string outputs.
type mockProposerEngine struct {
	// responses maps instruction text to the JSON output the engine returns.
	// A key of "" is a catch-all default.
	responses map[string]json.RawMessage
}

func (e *mockProposerEngine) Run(_ context.Context, instruction string, _ json.RawMessage) (json.RawMessage, error) {
	if r, ok := e.responses[instruction]; ok {
		return r, nil
	}
	if r, ok := e.responses[""]; ok {
		return r, nil
	}
	return json.RawMessage(`"default mock output"`), nil
}

// --- In-memory optimizer (no Postgres needed) --------------------------------

// optimizeInMemory is a test-only version of Optimize that accepts injected
// engines and a canned list of variant instructions instead of calling a real
// proposer model. This keeps the tests hermetic with zero network/DB access.
func optimizeInMemory(
	ctx context.Context,
	sk store.Skill,
	es store.Evalset,
	scoreEngine eval.Engine,
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := make([]byte, 0, 4)
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// --- Test helpers ------------------------------------------------------------

// makeEvalset builds a minimal Evalset with text-nonempty checks so we can
// control pass/fail by controlling what the score engine returns.
func makeEvalset(id string, caseIDs ...string) store.Evalset {
	es := store.Evalset{
		ID:       id,
		Name:     id,
		SkillRef: "test-skill",
	}
	for _, c := range caseIDs {
		es.Cases = append(es.Cases, store.EvalCase{
			ID:    c,
			Input: json.RawMessage(`{}`),
			Checks: []store.EvalCheckSpec{
				{Kind: "text-nonempty"},
			},
		})
	}
	return es
}

// scoreEngine builds a MockEngine that returns a non-empty string for the
// instructions in passing (pass=true) and an empty/null for the ones in
// failing (pass=false). We key by instruction text.
func scoreEngine(passing map[string]bool) eval.Engine {
	// The eval engine is called with instruction as the first arg.
	// MockEngine keys on instruction; we set the response per instruction.
	responses := make(map[string]json.RawMessage)
	for inst, pass := range passing {
		if pass {
			responses[inst] = json.RawMessage(`"good output"`)
		} else {
			responses[inst] = json.RawMessage(`""`)
		}
	}
	return &eval.MockEngine{Responses: responses}
}

// --- Tests -------------------------------------------------------------------

// TestOptimizePicksBetterVariant asserts:
//   - The optimizer scores baseline and 2 variants.
//   - Picks the variant with higher passRate.
//   - Computes gain correctly.
func TestOptimizePicksBetterVariant(t *testing.T) {
	ctx := context.Background()

	const (
		baseInst   = "baseline-instruction"
		betterInst = "better-instruction"
		worseInst  = "worse-instruction"
	)

	es := makeEvalset("es-pick", "c1", "c2")

	// baseline fails on c2 (1/2 pass), better passes both (2/2), worse fails both (0/2).
	eng := scoreEngine(map[string]bool{
		baseInst:   true, // catch-all: c1 passes, but we need per-case...
		betterInst: true,
		worseInst:  false,
	})
	// We need per-case control: use a custom engine.
	// For simplicity, MockEngine maps instruction→response and eval uses
	// instruction as the "target" arg. All cases share the same engine response.
	// So: baseline → "good output" on all cases → 100% pass; that's not quite what
	// we want. Let's use a more targeted approach with a custom engine that
	// also inspects the input case to decide pass/fail.

	// Simpler: use separate engines per variant + inject them.
	// Since eval.RunInMemory uses cfg.Engine.Run(ctx, instruction, caseInput),
	// and the mock maps instruction→response, we can make:
	//   - baseline instruction → empty string (fails text-nonempty)
	//   - better instruction   → "good output" (passes)
	//   - worse instruction    → empty string (fails)
	_ = eng // discard above

	scoreEng := &eval.MockEngine{Responses: map[string]json.RawMessage{
		baseInst:   json.RawMessage(`""`),     // empty → fails text-nonempty
		betterInst: json.RawMessage(`"good"`), // non-empty → passes
		worseInst:  json.RawMessage(`""`),     // empty → fails
	}}

	sk := store.Skill{ID: "test-skill", Instruction: baseInst}
	variantInstructions := []string{betterInst, worseInst}

	result, err := optimizeInMemory(ctx, sk, es, scoreEng, variantInstructions, 0)
	if err != nil {
		t.Fatalf("optimizeInMemory: %v", err)
	}

	// Baseline should fail all cases (empty output).
	if result.Baseline.PassRate != 0 {
		t.Errorf("expected baseline passRate=0, got %.2f", result.Baseline.PassRate)
	}

	// betterInst should pass all.
	if len(result.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(result.Variants))
	}
	better := result.Variants[0]
	worse := result.Variants[1]

	if better.PassRate != 1.0 {
		t.Errorf("better variant: expected passRate=1.0, got %.2f", better.PassRate)
	}
	if worse.PassRate != 0 {
		t.Errorf("worse variant: expected passRate=0, got %.2f", worse.PassRate)
	}

	// Winner should be the better variant.
	if result.Winner == nil {
		t.Fatal("expected a winner, got nil")
	}
	if result.Winner.Instruction != betterInst {
		t.Errorf("expected winner=%q, got %q", betterInst, result.Winner.Instruction)
	}
	if result.Gain != 1.0 {
		t.Errorf("expected gain=1.0, got %.2f", result.Gain)
	}
}

// TestOptimizeMinGainBlocks asserts that when no variant beats baseline by
// more than MinGain, no winner is selected.
func TestOptimizeMinGainBlocks(t *testing.T) {
	ctx := context.Background()

	const (
		baseInst   = "baseline-mintest"
		slightInst = "slight-improvement-mintest"
	)

	es := makeEvalset("es-mingain", "c1", "c2")

	// baseline: passes 1/2 cases (50%), slight: passes 2/2 cases (100%)
	// gain = +50%. With MinGain=0.6, the winner should be blocked.
	scoreEng := &eval.MockEngine{Responses: map[string]json.RawMessage{
		baseInst:   json.RawMessage(`""`),   // fails text-nonempty
		slightInst: json.RawMessage(`"ok"`), // passes text-nonempty
	}}

	sk := store.Skill{ID: "test-skill-mg", Instruction: baseInst}
	variantInstructions := []string{slightInst}

	// MinGain = 1.01: requires more than 100% gain, impossible → should block.
	result, err := optimizeInMemory(ctx, sk, es, scoreEng, variantInstructions, 1.01)
	if err != nil {
		t.Fatalf("optimizeInMemory: %v", err)
	}

	if result.Winner != nil {
		t.Errorf("expected no winner when gain=%.2f < minGain=1.01, but got winner %q",
			result.Gain, result.Winner.Instruction)
	}
	if result.Gain != 0 {
		t.Errorf("expected gain=0 when blocked, got %.2f", result.Gain)
	}
}

// TestOptimizeBaselineAlreadyPerfect asserts that when the baseline already
// achieves 100% pass rate, no variant can do better, and no winner is chosen.
func TestOptimizeBaselineAlreadyPerfect(t *testing.T) {
	ctx := context.Background()

	const (
		baseInst    = "perfect-baseline"
		variantInst = "variant-also-good"
	)

	es := makeEvalset("es-perfect", "c1")

	// Both pass — variant ties baseline, no gain → no winner (gain == minGain==0 means
	// we need strictly greater for passRate; check meanScore tie-break).
	scoreEng := &eval.MockEngine{Responses: map[string]json.RawMessage{
		baseInst:    json.RawMessage(`"good"`),
		variantInst: json.RawMessage(`"good"`),
	}}

	sk := store.Skill{ID: "test-skill-perfect", Instruction: baseInst}
	variantInstructions := []string{variantInst}

	result, err := optimizeInMemory(ctx, sk, es, scoreEng, variantInstructions, 0)
	if err != nil {
		t.Fatalf("optimizeInMemory: %v", err)
	}

	// Both pass 100% — variant ties baseline on passRate AND meanScore (both 1.0).
	// Our rule: gain > minGain OR (gain == minGain AND meanScore strictly better).
	// gain = 0, minGain = 0, meanScore also tied → no winner.
	if result.Winner != nil {
		t.Errorf("expected no winner when variant merely ties baseline, got %q",
			result.Winner.Instruction)
	}
}

// TestPickBestSelectsHighestPassRate tests pickBest directly.
func TestPickBestSelectsHighestPassRate(t *testing.T) {
	variants := []VariantScore{
		{Index: 1, Label: "v1", PassRate: 0.5, MeanScore: 0.8},
		{Index: 2, Label: "v2", PassRate: 0.9, MeanScore: 0.5},
		{Index: 3, Label: "v3", PassRate: 0.9, MeanScore: 0.9},
	}
	best := pickBest(variants)
	if best == nil {
		t.Fatal("pickBest returned nil")
	}
	// v3 has same passRate as v2 but higher meanScore.
	if best.Label != "v3" {
		t.Errorf("expected v3 (passRate=0.9, meanScore=0.9) to win, got %s", best.Label)
	}
}

// TestParseVariantArray tests the JSON array parser.
func TestParseVariantArray(t *testing.T) {
	cases := []struct {
		input   string
		want    []string
		wantErr bool
	}{
		{
			input: `["variant one", "variant two", "variant three"]`,
			want:  []string{"variant one", "variant two", "variant three"},
		},
		{
			input: `Here are the variants: ["a", "b"]`,
			want:  []string{"a", "b"},
		},
		{
			input:   "no array here",
			wantErr: true,
		},
		{
			input: `
Some preamble text.
["first improved instruction", "second improved instruction"]
Some postamble.`,
			want: []string{"first improved instruction", "second improved instruction"},
		},
	}

	for _, tc := range cases {
		variants, err := parseVariantArray(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("input %q: expected error, got variants %v", tc.input, variants)
			}
			continue
		}
		if err != nil {
			t.Errorf("input %q: unexpected error: %v", tc.input, err)
			continue
		}
		if len(variants) != len(tc.want) {
			t.Errorf("input %q: got %d variants, want %d", tc.input, len(variants), len(tc.want))
			continue
		}
		for i, v := range variants {
			if v != tc.want[i] {
				t.Errorf("input %q: variant[%d] = %q, want %q", tc.input, i, v, tc.want[i])
			}
		}
	}
}
