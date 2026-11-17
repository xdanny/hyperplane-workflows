package httpapi

// Learning — the two loops the platform runs on its own.
//
// Both were already built and neither ran by itself. Skill extraction waited
// for someone to click "extract" on a run; the improve pass waited for someone
// to run a CLI command. This file is the wiring that makes them automatic
// without moving a single judgement away from the human:
//
//   - an ACCEPTED delivery triggers extraction, and what comes back is a DRAFT
//     that waits in the inbox as a proposal (see internal/learning);
//   - a timer runs the improve pass, and what it applies is only ever a variant
//     that beat the current instruction on the skill's own evalset.
//
// Nothing here decides anything. It decides WHEN to ask.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/improve"
	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/learning"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// learnTimeout bounds one automatic extraction. It is generous because the
// extraction call itself is a high-reasoning codex run (six minutes), and the
// work happens after the HTTP response has already gone back.
const learnTimeout = 10 * time.Minute

// maxProposalInbox bounds the proposal cards the inbox shows at once.
const maxProposalInbox = 25

// cycleItemRef marks the synthetic first entry of an improve run's items — the
// one that names what triggered the pass rather than reporting on a skill. It
// is not a skill id and never collides with one (skill ids are slugs).
const cycleItemRef = "(cycle)"

// learnFromAcceptedDelivery is the hook the journey outcome handler calls when a
// human ACCEPTS a delivery. It returns immediately: extraction is a billed
// codex call, and the person recording a verdict should never wait on it.
//
// The context is deliberately NOT the request's — the request is over the
// moment the verdict is written, and inheriting its cancellation would kill
// every extraction the instant the response flushed.
func (s *Server) learnFromAcceptedDelivery(j journey.Journey) {
	if !s.cfg.AutoLearn {
		// A billed engine call nobody asked for is opt-out-able by design, and
		// a Config nobody configured (a test, an embedder) never spends one.
		log.Printf("learning: %s accepted — automatic extraction is off (HYPERPLANE_AUTO_LEARN)", j.Key)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), learnTimeout)
		defer cancel()

		res, err := learning.FromAcceptedDelivery(ctx, s.store, s.learningConfig(), j)
		switch {
		case errors.Is(err, learning.ErrAlreadyLearned):
			// Not a problem: this delivery's acceptance was already learned
			// from, and one acceptance may only ever cost one extraction.
			log.Printf("learning: %s already learned from (%s)", j.Key, res.RunID)
		case err != nil:
			log.Printf("learning: %s: %v", j.Key, err)
		default:
			log.Printf("learning: %s → %s (%s)", j.Key, res.Status, res.Message)
		}
	}()
}

// learningConfig resolves the paths extraction needs from server config.
func (s *Server) learningConfig() learning.Config {
	return learning.Config{
		SchemasDir: filepath.Join(s.cfg.PlatformRoot, "schemas"),
		TargetRoot: s.cfg.TargetRoot,
		RunsDir:    s.cfg.RunsDir,
	}
}

// listSkillProposals returns the draft skills the platform proposed to itself
// and nobody has ruled on — the inbox's third kind of "your turn".
//
// These are drafts, not a new record type: extraction already persists its
// proposal as a draft skill with the run it was learned from, and a draft is
// already inert until approved. Surfacing that same row is what makes the
// proposal card and the Knowledge registry agree by construction — approve in
// either place and it leaves both.
//
// Best-effort: a failure here must not take down the approvals, selections and
// deliveries the inbox primarily serves.
func (s *Server) listSkillProposals(ctx context.Context) []store.Skill {
	drafts, err := s.store.ListLearnedDrafts(ctx, maxProposalInbox)
	if err != nil {
		log.Printf("inbox: list skill proposals: %v", err)
		return []store.Skill{}
	}
	if drafts == nil {
		return []store.Skill{}
	}
	return s.markRehearsalProvenance(ctx, drafts)
}

// markRehearsalProvenance stamps `sourceRunRehearsal` onto every skill whose
// source runs were all dry runs, so a card can say what it was really learned
// from.
//
// Extraction refuses a rehearsal now, so nothing NEW can arrive in this state.
// The drafts already in the base did — one of them reads "Learned from a passed
// run you accepted" over a run in `mode: plan` that nobody ever decided — and
// they are not deleted, because a draft is a record of what the platform once
// proposed. What changes is what the card is allowed to claim about it.
//
// Computed at read time rather than backfilled: the run's mode is the fact, the
// metadata flag is a projection of it, and a projection that can drift from a
// column is how the disagreement started.
//
// Best-effort: a failed lookup leaves the skills exactly as stored. The cards
// then say no more than they always did, which is the safe direction — and the
// server-side approval refusal (knowledge.ErrRehearsalSource) does not depend on
// this at all.
func (s *Server) markRehearsalProvenance(ctx context.Context, skills []store.Skill) []store.Skill {
	ids := map[string]bool{}
	for _, sk := range skills {
		for _, r := range knowledge.SourceRuns(sk) {
			ids[r] = true
		}
	}
	if len(ids) == 0 {
		return skills
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	rehearsal, err := s.store.RehearsalRuns(ctx, list)
	if err != nil {
		log.Printf("skills: rehearsal provenance: %v", err)
		return skills
	}

	out := make([]store.Skill, 0, len(skills))
	for _, sk := range skills {
		runs := knowledge.SourceRuns(sk)
		if len(runs) == 0 {
			out = append(out, sk)
			continue
		}
		allRehearsed := true
		for _, r := range runs {
			if !rehearsal[r] {
				allRehearsed = false
				break
			}
		}
		if !allRehearsed {
			out = append(out, sk)
			continue
		}
		out = append(out, knowledge.WithMetadataFlag(sk, "sourceRunRehearsal", true))
	}
	return out
}

// ---- the improve loop -----------------------------------------------------

// improveConfig is the pass the automatic loop runs.
//
// Apply is TRUE, and that is not the loop deciding a rewrite is better:
// internal/optimize applies a variant only when it beat the current instruction
// on the skill's evalset. Refusing to apply a proven winner would leave the
// loop unable to improve anything at all, which is the state this feature
// exists to end. Every applied refinement is recorded in skill_refinements, so
// the lineage stays readable and reversible.
func improveConfig() improve.ImproveConfig {
	return improve.ImproveConfig{Apply: true}
}

// newImproveLoop builds the scheduler. It is constructed even when disabled,
// because the loop doubles as the non-overlap lock the manual trigger takes.
func newImproveLoop(s *Server, interval time.Duration) *improve.Loop {
	return &improve.Loop{
		Interval: interval,
		Cycle: func(ctx context.Context, trigger string) error {
			_, err := s.runImproveCycle(ctx, trigger)
			return err
		},
		Ready: func(ctx context.Context) bool {
			ok, err := improve.Harvestable(ctx, s.store, improveConfig())
			if err != nil {
				log.Printf("improve loop: harvest probe: %v", err)
				return false
			}
			return ok
		},
		Log: log.Printf,
	}
}

// StartLearning starts the background improve loop and states, at boot, which
// of the two automatic loops are on. Both spend billed engine time without
// anyone asking, so an operator should be able to read their posture off the
// log rather than out of the source.
//
// It is called by the API command, never by New, so tests and the CLI get a
// server with no timers running behind them.
func (s *Server) StartLearning(ctx context.Context) {
	if s.cfg.AutoLearn {
		log.Printf("auto-learning: on — an accepted delivery proposes a draft skill (HYPERPLANE_AUTO_LEARN=0 to disable)")
	} else {
		log.Printf("auto-learning: off (HYPERPLANE_AUTO_LEARN)")
	}
	s.improveLoop.Start(ctx)
}

// runImproveCycle runs one improve pass and journals it as an improve_runs row
// — the same record the manual trigger writes, so the Skill Lab's history shows
// automatic and manual passes in one list, each labelled with what triggered it.
func (s *Server) runImproveCycle(ctx context.Context, trigger string) (store.ImproveRun, error) {
	rec := store.ImproveRun{
		ID:     time.Now().Format("20060102-150405.000") + "-improve",
		Status: "running",
	}
	if _, err := s.store.CreateImproveRun(ctx, rec); err != nil {
		return rec, err
	}

	result, err := improve.Improve(ctx, s.store, improveConfig())

	rec.Status = "done"
	if err != nil {
		rec.Status = "failed"
	}
	rec.SkillsConsidered = result.SkillsConsidered
	rec.SkillsOptimized = result.SkillsOptimized
	rec.SkillsPromoted = result.SkillsPromoted
	rec.Items = improveItemsJSON(result.Items, trigger, err)
	if uerr := s.store.UpdateImproveRun(ctx, rec); uerr != nil {
		log.Printf("improve loop: record %s: %v", rec.ID, uerr)
	}
	log.Printf("improve %s (%s): considered %d, optimized %d, skipped %d, failed %d, promoted %d",
		rec.ID, trigger, rec.SkillsConsidered, rec.SkillsOptimized,
		result.SkillsSkipped, result.SkillsFailed, rec.SkillsPromoted)
	return rec, err
}

// improveItemsJSON encodes the per-skill outcomes, prefixed with a line naming
// what triggered the pass. The trigger lives in `items` rather than in a new
// column: it is a note about this pass, not a fact the schema needs to know,
// and a migration for a label would outlive the reason for it.
func improveItemsJSON(items []improve.ImproveItem, trigger string, err error) json.RawMessage {
	out := make([]improve.ImproveItem, 0, len(items)+1)
	head := improve.ImproveItem{SkillRef: cycleItemRef, Status: "trigger: " + trigger}
	if err != nil {
		head.Status += " — failed: " + err.Error()
	}
	out = append(out, head)
	out = append(out, items...)
	b, merr := json.Marshal(out)
	if merr != nil {
		return json.RawMessage("[]")
	}
	return b
}
