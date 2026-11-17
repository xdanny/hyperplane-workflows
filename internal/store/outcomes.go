package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OutcomeTag marks a decision row as a run's Outcome Decision — the human's
// final call on what a run delivered (accepted / changes requested / rejected).
// It is what separates outcome records from ADR-style decisions sharing the
// table, so both stay searchable and graphable without a second store.
const OutcomeTag = "outcome"

// outcomeIDPrefix namespaces outcome decision ids. Run ids are already
// path-safe (`20260704-182851.218-name`), so the id needs no escaping — and
// keeping it derivable means "does this run have an outcome?" is a primary-key
// lookup, not a scan.
const outcomeIDPrefix = "outcome-"

// OutcomeDecisionID is the stable decision id for a run's outcome. A run gets
// exactly one outcome, so this id doubles as the uniqueness constraint that
// makes recording an outcome insert-only (a second, different verdict must
// delete the decision first).
func OutcomeDecisionID(runID string) string { return outcomeIDPrefix + runID }

// CreateDecision inserts a decision and returns ErrExists when the id is
// already taken. It is the insert-only sibling of UpsertDecision, for records
// that must be final once written — an outcome is a commitment, not a draft.
func (s *Store) CreateDecision(ctx context.Context, d Decision) (Decision, error) {
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if d.Refs == nil {
		d.Refs = []string{}
	}
	// Auto-register the decision's project tag (no-op when unscoped).
	if err := s.EnsureProject(ctx, d.Project); err != nil {
		return Decision{}, err
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO decisions
			(id, title, status, context, decision, why, alternatives, consequences, tags, refs, project, source, decided_by, decided_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+decisionCols,
		d.ID, d.Title, statusOrAccepted(d.Status), d.Context, d.Decision, d.Why,
		d.Alternatives, d.Consequences, d.Tags, d.Refs, d.Project, d.Source, decidedByOrLocal(d.DecidedBy), d.DecidedAt)
	out, err := scanDecision(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING suppressed the insert — the id is taken.
		return out, ErrExists
	}
	if err != nil {
		return out, fmt.Errorf("create decision %q: %w", d.ID, err)
	}
	return out, nil
}

// GetRunOutcome returns the Outcome Decision recorded for a run, or ErrNotFound
// when the run has not been decided on yet.
func (s *Store) GetRunOutcome(ctx context.Context, runID string) (Decision, error) {
	return s.GetDecision(ctx, OutcomeDecisionID(runID))
}

// DeliveryDecisionForRun returns the verdict that GOVERNS a run's delivery, at
// either grain: the run's own outcome when somebody recorded one on this
// attempt, else the JOURNEY-grain outcome whose refs name this run.
//
// The second half is not an edge case — it is the ordinary one. The inbox asks
// for a verdict at journey grain (one question per delivery, not one per
// attempt), so most recorded decisions are journey-grain and a lookup keyed on
// `outcome-<runId>` finds nothing for them. Anything that asks "has this
// delivery been decided?" and only checks run grain will answer "no" about
// deliveries a person decided days ago.
//
// Run grain wins when both exist: a verdict somebody recorded on THIS attempt
// is the more specific statement about it.
//
// The journey-grain half is PINNED, and the pin is the `refs` match itself: a
// journey decision names every attempt that existed when it was written, so an
// attempt started afterwards is simply not in the list and this returns
// ErrNotFound for it — undecided, which is what it is. (See internal/journey/
// pin.go for the same rule stated over a whole delivery.) An acceptance is
// additionally refused on a REHEARSAL: a dry run produced nothing, both outcome
// endpoints refuse to record an acceptance on one, and inheriting one through
// this lookup would walk straight around that refusal.
func (s *Store) DeliveryDecisionForRun(ctx context.Context, runID string) (Decision, error) {
	d, err := s.GetRunOutcome(ctx, runID)
	if err == nil {
		return d, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return d, err
	}
	row := s.Pool.QueryRow(ctx, `
		SELECT `+decisionCols+` FROM decisions
		WHERE $1 = ANY(tags) AND $2 = ANY(refs)
		ORDER BY COALESCE(decided_at, created_at) DESC, id DESC
		LIMIT 1`, OutcomeTag, "run:"+runID)
	out, err := scanDecision(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("delivery decision for run %q: %w", runID, err)
	}
	if out.Decision == "accepted" {
		rehearsal, rerr := s.runIsRehearsal(ctx, runID)
		if rerr == nil && rehearsal {
			return Decision{}, ErrNotFound
		}
	}
	return out, nil
}

// runIsRehearsal reports whether one run was a dry run. Its own tiny query
// because DeliveryDecisionForRun is handed a run id, not a run, and the only
// thing it needs to know is the mode.
func (s *Store) runIsRehearsal(ctx context.Context, runID string) (bool, error) {
	var mode string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(mode,'') FROM runs WHERE run_id = $1`, runID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("run mode %q: %w", runID, err)
	}
	return IsRehearsalMode(mode), nil
}

// The "needs decision" queue used to be a run-grain SQL anti-join here:
// terminal top-level runs with no `outcome-<runId>` decision. It is now derived
// at JOURNEY grain instead (internal/journey), because run grain asked the wrong
// question — seven failed runs that are really two products demanded seven
// verdicts. The rules that query encoded did not go away, they moved:
//
//   - process sub-runs never queue (RunFilter.TopLevel);
//   - a repaired run never queues (journey.Attempt.Superseded);
//   - `interrupted` never queues — cancelling a run IS the human's decision, so
//     re-queuing it would be busywork (journey.StageStopped);
//   - an already-decided delivery never queues (journey.Journey.NeedsDecision).
//
// The journey layer adds the rule run grain could not express at all: an attempt
// that a NEWER attempt of the same delivery has superseded stops asking too.
