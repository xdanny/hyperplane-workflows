package store

import (
	"context"
	"fmt"
	"strings"
)

// Rehearsals — one definition of "this run did not really happen".
//
// engine.runMode stamps the mode column at creation: a dry run is "plan" (it
// planned the graph and let every agent node answer with a stand-in), a real one
// is "run". Three surfaces used to answer the question separately — the outcome
// brief had its own predicate, the run page had none, and skill extraction never
// asked at all — which is how a rehearsal came to be shown as PASSED · DELIVERY
// ACCEPTED · 4/4 PROVEN and then learned from as if it were evidence.
//
// The rule the predicate exists to enforce: a rehearsal's outputs are stand-ins.
// They may be read, they may be shown, and they may be labelled — they may never
// be counted as proof, accepted as a delivery, or learned from.

// RehearsalSentence is the plain-English refusal, kept here so the API, the CLI
// and the UI all say the same thing rather than three near-misses.
const RehearsalSentence = "This attempt was a rehearsal; its results are stand-ins and cannot be accepted as a delivery. Run it for real first."

// RehearsalChip is the short label a surface puts next to a rehearsal's state.
const RehearsalChip = "REHEARSAL — stand-in results, not evidence"

// RehearsalModes is every run mode that names a dry run.
//
// It is a slice rather than a switch so that SQL can be handed the same list
// the Go predicate uses: "has this recipe ever run FOR REAL" is asked in a
// window function, and a second copy of these four words in a query is a second
// place for them to drift.
var RehearsalModes = []string{"plan", "dry-run", "dryrun", "rehearsal"}

// IsRehearsalMode reports whether a run mode names a dry run.
//
// The spellings beyond "plan" are accepted because nothing stops an embedder
// writing the word it means; "" and "run" are the real thing.
func IsRehearsalMode(mode string) bool {
	m := strings.ToLower(strings.TrimSpace(mode))
	for _, r := range RehearsalModes {
		if m == r {
			return true
		}
	}
	return false
}

// IsRehearsal reports whether a run was a dry run.
func IsRehearsal(r Run) bool { return IsRehearsalMode(r.Mode) }

// RehearsalRuns reports, for each id, whether that run was a rehearsal — in one
// query.
//
// It exists for the surfaces that hold a LIST of run ids and have to say
// something honest about each: the learned-drafts inbox above all, where the
// only thing a card knows about its provenance is a run id it never fetched.
// Ids that do not exist are simply absent from the result — a card about a run
// nobody can find is not entitled to call it real, and not entitled to call it
// a rehearsal either.
func (s *Store) RehearsalRuns(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT run_id, COALESCE(mode,'') FROM runs WHERE run_id = ANY($1)`, ids)
	if err != nil {
		return out, fmt.Errorf("rehearsal runs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, mode string
		if err := rows.Scan(&id, &mode); err != nil {
			return out, fmt.Errorf("rehearsal runs: scan: %w", err)
		}
		out[id] = IsRehearsalMode(mode)
	}
	return out, rows.Err()
}
