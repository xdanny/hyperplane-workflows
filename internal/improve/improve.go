// Package improve implements the always-on self-improvement loop (M26).
// It discovers skills with sufficient feedback, runs the optimize loop for
// each, and optionally applies winning instructions back to the skill store.
//
// Flow: discover skills (or use explicit list) → for each: harvest feedback →
// if enough feedback: optimize → if winner beats baseline and Apply=true:
// confirm optimizer application receipt → record per-skill outcome.
package improve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Store is the minimal interface the improve loop needs from the data layer.
// Using an interface (rather than *store.Store directly) makes the package
// hermetically testable with a fake.
type Store interface {
	ListSkills(ctx context.Context) ([]store.Skill, error)
}

// ImproveConfig controls a single Improve pass.
type ImproveConfig struct {
	// Skills is the explicit list of skill IDs to consider. When empty, all
	// skills are discovered and those with >= MinFeedback items are included.
	Skills []string

	// MinFeedback is the minimum number of FeedbackItems a skill must have
	// before it is eligible for optimization. Defaults to 3.
	MinFeedback int

	// MaxSkills caps the number of skills processed in one pass. 0 means unlimited.
	MaxSkills int

	// Apply: if true, the winning instruction is written back to the skill store.
	// If false, winners are proposed but not persisted.
	Apply bool

	// Engine is passed through to optimize.Config.ScoreEngine.
	Engine string

	// DryRun skips the optimize call and marks every skill "skipped: dry-run".
	DryRun bool

	// HarvestFn is the feedback-harvesting function. When nil it defaults to
	// a thin adapter around optimize.HarvestFeedback. Injectable for tests.
	HarvestFn func(ctx context.Context, st *store.Store, skillRef string) ([]optimize.FeedbackItem, error)

	// OptimizeFn is the optimize function. When nil it defaults to optimize.Optimize.
	// Injectable for tests.
	OptimizeFn func(ctx context.Context, st *store.Store, cfg optimize.Config) (optimize.Result, error)
}

func (c *ImproveConfig) minFeedback() int {
	if c.MinFeedback <= 0 {
		return 3
	}
	return c.MinFeedback
}

// Status values and prefixes for ImproveItem.Status.
//
// The status string is not a machine code: it is the sentence the Skill Lab
// renders under the skill's name, so it is written for the person reading it.
// The two prefixes are what the counters classify on, which is why every status
// must start with one of them or be one of the three optimized outcomes below.
const (
	// StatusSkipPrefix marks a skill the pass deliberately did not optimize.
	// A skip is not a failure and is never counted as refined.
	StatusSkipPrefix = "skipped: "
	// StatusErrorPrefix marks a skill whose optimization actually broke.
	StatusErrorPrefix = "error: "

	// StatusNoEvalset is the skip that dominates a fresh install: the skill has
	// no evalset, so there is nothing to score a rewrite against. It says so in
	// the reader's terms — nobody outside the CLI can "pass --evalset".
	StatusNoEvalset = StatusSkipPrefix + "no evalset yet — this skill has no practice test to score rewrites against"
	// StatusInsufficientFeedback is the skip for a skill nobody has judged enough
	// times yet for a rewrite to be grounded in anything.
	StatusInsufficientFeedback = StatusSkipPrefix + "not enough feedback yet"
	// StatusDryRun is the skip a dry-run pass records for every skill.
	StatusDryRun = StatusSkipPrefix + "dry run — nothing was optimized"

	// StatusPromoted / StatusProposed / StatusNoImprovement are the three ways a
	// skill that WAS optimized can end. All three count as refined: the loop ran
	// the variants and scored them.
	StatusPromoted      = "promoted"
	StatusProposed      = "proposed (not applied)"
	StatusNoImprovement = "no improvement"
)

// ImproveItem is the per-skill outcome recorded in an improve pass.
type ImproveItem struct {
	SkillRef string `json:"skillRef"`
	Status   string `json:"status"`
	Winner   string `json:"winner,omitempty"` // non-empty when a winning variant was found
}

// Skipped reports whether this skill was deliberately not optimized.
func (i ImproveItem) Skipped() bool { return strings.HasPrefix(i.Status, StatusSkipPrefix) }

// Failed reports whether optimizing this skill broke.
func (i ImproveItem) Failed() bool { return strings.HasPrefix(i.Status, StatusErrorPrefix) }

// ImproveResult summarises the full improve pass.
//
// The counters reconcile by construction:
//
//	SkillsConsidered == SkillsOptimized + SkillsSkipped + SkillsFailed
//
// which is the whole point of them. A panel that said "considered 12, refined
// 12, applied 0" over twelve rows that each carried an error was counting every
// skill it TOUCHED as refined; a skill the loop could not score was never
// refined, and saying otherwise made the one honest number (applied 0) look
// like a bug instead of the truth.
type ImproveResult struct {
	SkillsConsidered int `json:"skillsConsidered"`
	// SkillsOptimized is skills whose variants were actually proposed and
	// scored — refined, whether or not a variant won.
	SkillsOptimized int `json:"skillsOptimized"`
	// SkillsSkipped is skills the pass deliberately left alone (no evalset, not
	// enough feedback, dry run).
	SkillsSkipped int `json:"skillsSkipped"`
	// SkillsFailed is skills whose optimization broke.
	SkillsFailed int `json:"skillsFailed"`
	// SkillsPromoted is the subset of SkillsOptimized whose winner was applied.
	SkillsPromoted int           `json:"skillsPromoted"`
	Items          []ImproveItem `json:"items"`
}

// improveStore is an internal adaptor that lets Improve forward Store calls to
// optimize.HarvestFeedback and optimize.Optimize, which expect *store.Store.
// When the caller injects HarvestFn/OptimizeFn those are used instead.
//
// The improve.Store interface satisfies testing; real callers pass a *store.Store
// directly (which implements improve.Store).

// realHarvest is the default HarvestFn: wraps optimize.HarvestFeedback.
// It panics at build time (via the type assertion below) if the real store ever
// diverges from the interface; at runtime it type-asserts so tests can inject
// a fake.
func realHarvest(ctx context.Context, st *store.Store, skillRef string) ([]optimize.FeedbackItem, error) {
	return optimize.HarvestFeedback(ctx, st, skillRef)
}

// realOptimize is the default OptimizeFn.
func realOptimize(ctx context.Context, st *store.Store, cfg optimize.Config) (optimize.Result, error) {
	return optimize.Optimize(ctx, st, cfg)
}

// Improve runs one self-improvement pass. st must implement improve.Store;
// when HarvestFn/OptimizeFn are nil the real store (*store.Store) is required
// because the default implementations need the full Store. When testing, inject
// both functions and pass any Store implementation.
func Improve(ctx context.Context, st Store, cfg ImproveConfig) (ImproveResult, error) {
	minFB := cfg.minFeedback()

	// Resolve harvest and optimize functions.
	harvestFn := cfg.HarvestFn
	optimizeFn := cfg.OptimizeFn

	// If using default functions, we need the concrete *store.Store.
	var realSt *store.Store
	if harvestFn == nil || optimizeFn == nil {
		rs, ok := st.(*store.Store)
		if !ok {
			return ImproveResult{}, fmt.Errorf("improve: HarvestFn and OptimizeFn must be set when st is not *store.Store")
		}
		realSt = rs
		if harvestFn == nil {
			harvestFn = realHarvest
		}
		if optimizeFn == nil {
			optimizeFn = realOptimize
		}
	}

	// Discover skill IDs to consider.
	skillIDs := cfg.Skills
	if len(skillIDs) == 0 {
		skills, err := st.ListSkills(ctx)
		if err != nil {
			return ImproveResult{}, fmt.Errorf("improve: list skills: %w", err)
		}
		for _, sk := range skills {
			skillIDs = append(skillIDs, sk.ID)
		}
	}

	// Apply MaxSkills cap.
	if cfg.MaxSkills > 0 && len(skillIDs) > cfg.MaxSkills {
		skillIDs = skillIDs[:cfg.MaxSkills]
	}

	var result ImproveResult
	result.SkillsConsidered = len(skillIDs)

	for _, skillRef := range skillIDs {
		item := runSkill(ctx, realSt, skillRef, minFB, cfg, harvestFn, optimizeFn)
		result.Items = append(result.Items, item)
		switch {
		case item.Skipped():
			result.SkillsSkipped++
		case item.Failed():
			result.SkillsFailed++
		default:
			result.SkillsOptimized++
		}
		if item.Status == StatusPromoted {
			result.SkillsPromoted++
		}
	}

	return result, nil
}

// runSkill runs one skill through the harvest → optimize → promote pipeline and
// returns the per-skill outcome.
func runSkill(
	ctx context.Context,
	realSt *store.Store,
	skillRef string,
	minFeedback int,
	cfg ImproveConfig,
	harvestFn func(context.Context, *store.Store, string) ([]optimize.FeedbackItem, error),
	optimizeFn func(context.Context, *store.Store, optimize.Config) (optimize.Result, error),
) ImproveItem {
	item := ImproveItem{SkillRef: skillRef}

	if cfg.DryRun {
		item.Status = StatusDryRun
		return item
	}

	// Step 1: harvest feedback.
	feedback, err := harvestFn(ctx, realSt, skillRef)
	if err != nil {
		item.Status = StatusErrorPrefix + "could not read this skill's feedback: " + err.Error()
		return item
	}
	if len(feedback) < minFeedback {
		item.Status = StatusInsufficientFeedback
		return item
	}

	// Step 2: optimize.
	optCfg := optimize.Config{
		SkillID:     skillRef,
		Apply:       cfg.Apply,
		ScoreEngine: cfg.Engine,
		FromRuns:    true,
	}
	res, err := optimizeFn(ctx, realSt, optCfg)
	if errors.Is(err, optimize.ErrNoFeedback) {
		item.Status = StatusInsufficientFeedback
		return item
	}
	if errors.Is(err, optimize.ErrNoEvalset) {
		// Not a failure. A skill with no evalset has nothing to score a rewrite
		// against, so the pass leaves it alone — and says that in words the
		// person reading the Skill Lab can act on.
		item.Status = StatusNoEvalset
		return item
	}
	if err != nil {
		item.Status = StatusErrorPrefix + "could not rewrite this skill: " + err.Error()
		return item
	}

	// Step 3: decide outcome.
	if res.Winner == nil {
		item.Status = StatusNoImprovement
		return item
	}

	item.Winner = res.Winner.Label
	if cfg.Apply {
		// Optimize owns application against its evaluated snapshot. Reapplying
		// here would overwrite an edit made after that conditional write.
		if !res.Applied {
			item.Status = StatusErrorPrefix + "optimizer did not confirm applying its winning instruction"
			return item
		}
		item.Status = StatusPromoted
	} else {
		item.Status = StatusProposed
	}
	return item
}
