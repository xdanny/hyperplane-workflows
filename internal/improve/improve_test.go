package improve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// fakeStore implements improve.Store in memory.
type fakeStore struct {
	skills       map[string]store.Skill
	upsertCalled []string
}

func newFakeStore(ids ...string) *fakeStore {
	fs := &fakeStore{skills: make(map[string]store.Skill)}
	for _, id := range ids {
		fs.skills[id] = store.Skill{ID: id, Name: id, Instruction: "original-" + id}
	}
	return fs
}

func (fs *fakeStore) ListSkills(_ context.Context) ([]store.Skill, error) {
	out := make([]store.Skill, 0, len(fs.skills))
	for _, sk := range fs.skills {
		out = append(out, sk)
	}
	return out, nil
}

func (fs *fakeStore) GetSkill(_ context.Context, id string) (store.Skill, error) {
	sk, ok := fs.skills[id]
	if !ok {
		return store.Skill{}, store.ErrNotFound
	}
	return sk, nil
}

func (fs *fakeStore) UpsertSkill(_ context.Context, sk store.Skill) error {
	fs.skills[sk.ID] = sk
	fs.upsertCalled = append(fs.upsertCalled, sk.ID)
	return nil
}

// makeHarvestFn returns a harvest function that always returns n dummy items.
func makeHarvestFn(n int) func(context.Context, *store.Store, string) ([]optimize.FeedbackItem, error) {
	return func(_ context.Context, _ *store.Store, _ string) ([]optimize.FeedbackItem, error) {
		items := make([]optimize.FeedbackItem, n)
		for i := range items {
			items[i] = optimize.FeedbackItem{Source: "gate", Passed: false, Reasons: []string{"reason"}}
		}
		return items, nil
	}
}

// makeOptimizeFn returns an optimize function that reports a winner if hasWinner is true.
func makeOptimizeFn(hasWinner bool) func(context.Context, *store.Store, optimize.Config) (optimize.Result, error) {
	return func(_ context.Context, _ *store.Store, cfg optimize.Config) (optimize.Result, error) {
		res := optimize.Result{SkillID: cfg.SkillID}
		if hasWinner {
			winner := optimize.VariantScore{
				Index:       1,
				Label:       "variant-1",
				Instruction: "improved-" + cfg.SkillID,
				PassRate:    0.9,
			}
			res.Winner = &winner
			res.Gain = 0.1
			res.Applied = cfg.Apply
		}
		return res, nil
	}
}

// TestSkillBelowMinFeedback verifies that a skill with too little feedback is skipped.
func TestSkillBelowMinFeedback(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore("skill-a")

	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      []string{"skill-a"},
		MinFeedback: 5,
		HarvestFn:   makeHarvestFn(2), // below threshold
		OptimizeFn:  makeOptimizeFn(true),
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	got := result.Items[0]
	if got.Status != StatusInsufficientFeedback {
		t.Errorf("expected %q, got %q", StatusInsufficientFeedback, got.Status)
	}
	if result.SkillsSkipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.SkillsSkipped)
	}
	if result.SkillsOptimized != 0 {
		t.Errorf("expected 0 optimized, got %d", result.SkillsOptimized)
	}
	if result.SkillsPromoted != 0 {
		t.Errorf("expected 0 promoted, got %d", result.SkillsPromoted)
	}
	if len(fs.upsertCalled) != 0 {
		t.Errorf("expected UpsertSkill not called, got %v", fs.upsertCalled)
	}
}

// TestApplyPromotesWinner trusts the optimizer application receipt without a second write.
func TestApplyPromotesWinner(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore("skill-b")

	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      []string{"skill-b"},
		MinFeedback: 3,
		Apply:       true,
		HarvestFn:   makeHarvestFn(5),
		OptimizeFn:  makeOptimizeFn(true),
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	got := result.Items[0]
	if got.Status != "promoted" {
		t.Errorf("expected 'promoted', got %q", got.Status)
	}
	if got.Winner != "variant-1" {
		t.Errorf("expected winner='variant-1', got %q", got.Winner)
	}
	if result.SkillsPromoted != 1 {
		t.Errorf("expected 1 promoted, got %d", result.SkillsPromoted)
	}
	if len(fs.upsertCalled) != 0 {
		t.Error("improve repeated the optimizer write")
	}
}

// TestProposedNotApplied verifies that Apply=false with a winner records
// "proposed (not applied)" and does not call UpsertSkill.
func TestProposedNotApplied(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore("skill-c")

	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      []string{"skill-c"},
		MinFeedback: 3,
		Apply:       false,
		HarvestFn:   makeHarvestFn(5),
		OptimizeFn:  makeOptimizeFn(true),
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	got := result.Items[0]
	if got.Status != "proposed (not applied)" {
		t.Errorf("expected 'proposed (not applied)', got %q", got.Status)
	}
	if result.SkillsPromoted != 0 {
		t.Errorf("expected 0 promoted, got %d", result.SkillsPromoted)
	}
	// UpsertSkill must NOT have been called.
	if len(fs.upsertCalled) != 0 {
		t.Errorf("expected UpsertSkill not called, got %v", fs.upsertCalled)
	}
}

// TestMaxSkillsRespected verifies that MaxSkills caps the number of skills processed.
func TestMaxSkillsRespected(t *testing.T) {
	ctx := context.Background()
	// Provide 4 skills explicitly so ordering is deterministic.
	skills := []string{"sk1", "sk2", "sk3", "sk4"}
	fs := newFakeStore(skills...)

	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      skills,
		MaxSkills:   2,
		MinFeedback: 1,
		HarvestFn:   makeHarvestFn(3),
		OptimizeFn:  makeOptimizeFn(false),
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if result.SkillsConsidered != 2 {
		t.Errorf("expected SkillsConsidered=2, got %d", result.SkillsConsidered)
	}
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}
}

// TestNoWinner verifies that when optimize returns no winner the status is "no improvement".
func TestNoWinner(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore("skill-d")

	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      []string{"skill-d"},
		MinFeedback: 3,
		Apply:       true,
		HarvestFn:   makeHarvestFn(5),
		OptimizeFn:  makeOptimizeFn(false), // no winner
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	got := result.Items[0]
	if got.Status != StatusNoImprovement {
		t.Errorf("expected %q, got %q", StatusNoImprovement, got.Status)
	}
	if result.SkillsPromoted != 0 {
		t.Errorf("expected 0 promoted, got %d", result.SkillsPromoted)
	}
	// Scored and lost is still refined: the loop did the work.
	if result.SkillsOptimized != 1 {
		t.Errorf("expected 1 optimized, got %d", result.SkillsOptimized)
	}
}

// makeErrOptimizeFn returns an optimize function that always fails with err.
func makeErrOptimizeFn(err error) func(context.Context, *store.Store, optimize.Config) (optimize.Result, error) {
	return func(_ context.Context, _ *store.Store, cfg optimize.Config) (optimize.Result, error) {
		return optimize.Result{SkillID: cfg.SkillID}, err
	}
}

// TestSkillWithoutEvalsetIsSkippedNotFailed is the audit's worst counter bug in
// one case: a skill with no evalset was recorded as an ERROR row carrying CLI
// advice, and counted as REFINED. It is neither. It is a skip, said in words a
// person reading the Skill Lab can act on.
func TestSkillWithoutEvalsetIsSkippedNotFailed(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore("ai-engineering")

	noEvalset := fmt.Errorf("%w: no evalset has skill_ref=%q", optimize.ErrNoEvalset, "ai-engineering")
	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      []string{"ai-engineering"},
		MinFeedback: 3,
		HarvestFn:   makeHarvestFn(5),
		OptimizeFn:  makeErrOptimizeFn(noEvalset),
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	got := result.Items[0]

	if got.Status != StatusNoEvalset {
		t.Errorf("status = %q, want %q", got.Status, StatusNoEvalset)
	}
	if !got.Skipped() || got.Failed() {
		t.Errorf("a skill with no evalset must classify as a skip, got %q", got.Status)
	}
	// The status is what the Skill Lab renders. It must not tell a person to
	// pass a flag to a CLI they are not using.
	if strings.Contains(got.Status, "--evalset") || strings.Contains(got.Status, "skill_ref=") {
		t.Errorf("status leaks CLI/DB advice into the UI: %q", got.Status)
	}
	if result.SkillsOptimized != 0 {
		t.Errorf("a skipped skill was counted as refined: optimized=%d", result.SkillsOptimized)
	}
	if result.SkillsSkipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.SkillsSkipped)
	}
	if result.SkillsFailed != 0 {
		t.Errorf("expected 0 failed, got %d", result.SkillsFailed)
	}
}

// TestCountersReconcile is the arithmetic the panel was getting wrong:
// considered = refined + skipped + failed, over a pass that hits every branch.
func TestCountersReconcile(t *testing.T) {
	ctx := context.Background()
	skills := []string{"refined-one", "no-evalset", "thin-feedback", "broken"}
	fs := newFakeStore(skills...)

	noEvalset := fmt.Errorf("%w: no evalset has skill_ref=%q", optimize.ErrNoEvalset, "no-evalset")
	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:      skills,
		MinFeedback: 3,
		Apply:       true,
		HarvestFn: func(_ context.Context, _ *store.Store, skillRef string) ([]optimize.FeedbackItem, error) {
			n := 5
			if skillRef == "thin-feedback" {
				n = 1
			}
			return makeHarvestFn(n)(ctx, nil, skillRef)
		},
		OptimizeFn: func(c context.Context, st *store.Store, cfg optimize.Config) (optimize.Result, error) {
			switch cfg.SkillID {
			case "no-evalset":
				return optimize.Result{SkillID: cfg.SkillID}, noEvalset
			case "broken":
				return optimize.Result{SkillID: cfg.SkillID}, errors.New("scoring engine unreachable")
			default:
				return makeOptimizeFn(true)(c, st, cfg)
			}
		},
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}

	if result.SkillsConsidered != 4 {
		t.Fatalf("considered = %d, want 4", result.SkillsConsidered)
	}
	if result.SkillsOptimized != 1 {
		t.Errorf("optimized = %d, want 1 (only refined-one was scored)", result.SkillsOptimized)
	}
	if result.SkillsSkipped != 2 {
		t.Errorf("skipped = %d, want 2 (no-evalset, thin-feedback)", result.SkillsSkipped)
	}
	if result.SkillsFailed != 1 {
		t.Errorf("failed = %d, want 1 (broken)", result.SkillsFailed)
	}
	if result.SkillsPromoted != 1 {
		t.Errorf("promoted = %d, want 1", result.SkillsPromoted)
	}
	if sum := result.SkillsOptimized + result.SkillsSkipped + result.SkillsFailed; sum != result.SkillsConsidered {
		t.Errorf("counters do not reconcile: considered=%d, optimized+skipped+failed=%d",
			result.SkillsConsidered, sum)
	}
	// Promoted is a subset of optimized, never its own bucket.
	if result.SkillsPromoted > result.SkillsOptimized {
		t.Errorf("promoted=%d exceeds optimized=%d", result.SkillsPromoted, result.SkillsOptimized)
	}
}

// TestDryRunCountsAsSkipped: a dry run refines nothing, and must not claim to.
func TestDryRunCountsAsSkipped(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore("skill-e")

	result, err := Improve(ctx, fs, ImproveConfig{
		Skills:     []string{"skill-e"},
		DryRun:     true,
		HarvestFn:  makeHarvestFn(9),
		OptimizeFn: makeOptimizeFn(true),
	})
	if err != nil {
		t.Fatalf("Improve error: %v", err)
	}
	if result.SkillsOptimized != 0 || result.SkillsSkipped != 1 {
		t.Errorf("dry run: optimized=%d skipped=%d, want 0/1",
			result.SkillsOptimized, result.SkillsSkipped)
	}
	if !result.Items[0].Skipped() {
		t.Errorf("dry-run status %q must classify as a skip", result.Items[0].Status)
	}
}

func TestFeedbackDisappearingBeforeOptimizeIsSkipped(t *testing.T) {
	result, err := Improve(t.Context(), newFakeStore("lost-feedback"), ImproveConfig{
		Skills: []string{"lost-feedback"}, MinFeedback: 1, HarvestFn: makeHarvestFn(1), OptimizeFn: makeErrOptimizeFn(optimize.ErrNoFeedback),
	})
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != StatusInsufficientFeedback || !result.Items[0].Skipped() || result.SkillsOptimized != 0 {
		t.Fatalf("feedback race became failure: %+v %v", result, err)
	}
}

func TestApplyPreservesEditAfterOptimizerReceipt(t *testing.T) {
	fs := newFakeStore("skill-later-edit")
	result, err := Improve(t.Context(), fs, ImproveConfig{
		Skills: []string{"skill-later-edit"}, MinFeedback: 1, Apply: true, HarvestFn: makeHarvestFn(1),
		OptimizeFn: func(ctx context.Context, st *store.Store, cfg optimize.Config) (optimize.Result, error) {
			res, err := makeOptimizeFn(true)(ctx, st, cfg)
			// A user saves again after the optimizer applied its result.
			sk := fs.skills[cfg.SkillID]
			sk.Instruction = "newer human edit"
			sk.Metadata = store.JSON(`{"status":"draft","note":"preserved"}`)
			fs.skills[cfg.SkillID] = sk
			return res, err
		},
	})
	if err != nil || result.SkillsPromoted != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	sk := fs.skills["skill-later-edit"]
	if sk.Instruction != "newer human edit" || string(sk.Metadata) != `{"status":"draft","note":"preserved"}` || len(fs.upsertCalled) != 0 {
		t.Fatalf("improve overwrote newer edit: %+v writes=%v", sk, fs.upsertCalled)
	}
}

func TestApplyRequiresOptimizerReceipt(t *testing.T) {
	fs := newFakeStore("skill-no-receipt")
	result, err := Improve(t.Context(), fs, ImproveConfig{
		Skills: []string{"skill-no-receipt"}, MinFeedback: 1, Apply: true, HarvestFn: makeHarvestFn(1),
		OptimizeFn: func(ctx context.Context, st *store.Store, cfg optimize.Config) (optimize.Result, error) {
			res, err := makeOptimizeFn(true)(ctx, st, cfg)
			res.Applied = false
			return res, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillsPromoted != 0 || !strings.Contains(result.Items[0].Status, "did not confirm") || len(fs.upsertCalled) != 0 {
		t.Fatalf("missing receipt counted as promotion: %+v", result)
	}
}
