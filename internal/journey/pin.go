package journey

// pin.go — a recorded verdict covers the attempts that EXISTED when it was
// written, and nothing else.
//
// The bug this file ends: a delivery accepted on 2026-08-21, when it had two
// attempts, was still headed "DELIVERY ACCEPTED · covering all 5 attempts" on
// 2026-08-22 — including on attempt 5, a rehearsal that failed at its inputs
// gate. The journey key is derived from (project, dag, recipe|chain-root), so
// every later attempt joins the same journey, and the verdict simply flowed
// forward onto work nobody had seen. A signature that grows to cover whatever
// is produced next is not a signature.
//
// The pin is not new information: the journey-grain decision already names each
// attempt in its `refs` ("run:<id>") at the moment it is recorded, precisely so
// each run stays reachable from it. That list IS the coverage set. A decision
// that names no runs at all — an older record, or one written by hand — falls
// back to its timestamp: an attempt that started after the verdict was decided
// is newer than the verdict.
//
// One extra rule rides here because it is the same mistake wearing a different
// hat: a REHEARSAL never inherits an acceptance. A dry run produced nothing, so
// there is nothing under it for an "accepted" to be about — the run-grain and
// journey-grain endpoints both already refuse to record one (store.RehearsalSentence),
// and a verdict that reaches one by inheritance would walk straight around that
// refusal.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// runRefPrefix is how a decision names a run in its refs.
const runRefPrefix = "run:"

// Coverage says which attempts a recorded journey-grain verdict actually
// covered, and which ones came after it.
//
// It is carried on the wire because the honest sentence needs all of it:
// "Accepted on 2026-08-21 covering attempts 1–2; attempts 3–5 are newer than
// that verdict and undecided." A boolean could not say that.
type Coverage struct {
	// Attempts are the chapter numbers the verdict covered, ascending.
	Attempts []int `json:"attempts"`
	// Undecided are the chapter numbers that came after it, ascending. Non-empty
	// is exactly the condition under which the verdict no longer governs.
	Undecided []int `json:"undecided"`
	// CoversNewest is true when the delivery's current state is the state the
	// verdict was given on — the only case in which it still governs.
	//
	// "Current state" means the newest attempt that COULD be decided on: a
	// rehearsal is a dry run, not an attempt at delivering, so a delivery that
	// was really run and rehearsed afterwards is still governed by the verdict
	// given on the real attempt. A delivery whose every attempt was a rehearsal
	// has no decidable attempt at all, and nothing governs it.
	CoversNewest bool `json:"coversNewest"`
	// Basis is how the pin was resolved: "refs" when the decision named its
	// attempts (every decision this platform writes does), "time" when it named
	// none and its timestamp had to stand in.
	Basis string `json:"basis"`
	// DecidedAt is the verdict's own time, so a caller can print it without
	// reaching back to the outcome.
	DecidedAt time.Time `json:"decidedAt"`
}

// coverageBasisRefs / coverageBasisTime are Coverage.Basis's two values.
const (
	coverageBasisRefs = "refs"
	coverageBasisTime = "time"
)

// pinnedRuns returns the run ids a decision named, as a set. Empty means the
// decision named none — see Coverage.Basis.
func pinnedRuns(d *store.Decision) map[string]bool {
	if d == nil {
		return nil
	}
	out := map[string]bool{}
	for _, ref := range d.Refs {
		if id := strings.TrimPrefix(ref, runRefPrefix); id != ref && strings.TrimSpace(id) != "" {
			out[strings.TrimSpace(id)] = true
		}
	}
	return out
}

// decisionTime is when a decision was decided, falling back to when the row was
// written.
func decisionTime(d store.Decision) time.Time {
	if d.DecidedAt != nil {
		return *d.DecidedAt
	}
	return d.CreatedAt
}

// covers reports whether one attempt falls inside a decision's pin.
//
// `pinned` is the decision's named-run set; when it is empty the decision named
// no runs and `at` (its timestamp) decides instead: an attempt that started
// strictly after the verdict is newer than it. An attempt with no start time is
// treated as covered under the time fallback, because "we do not know when this
// began" is not grounds to invent a gap in an existing signature — the refs
// path, which every decision this platform writes takes, never needs the guess.
func covers(a Attempt, pinned map[string]bool, at time.Time, verdict string) bool {
	// A rehearsal never inherits an acceptance, whatever the refs say. See the
	// file comment.
	if a.Rehearsal && verdict == verdictAccepted {
		return false
	}
	if len(pinned) > 0 {
		return pinned[a.RunID]
	}
	if a.StartedAt == nil {
		return true
	}
	return !a.StartedAt.After(at)
}

// verdictAccepted is the one verdict a rehearsal cannot inherit.
const verdictAccepted = "accepted"

// pin computes a journey-grain decision's coverage over the delivery's attempts
// and stamps Attempt.DecisionCovered. `attempts` is newest-first, as Build
// builds it.
func pin(attempts []Attempt, d *store.Decision) *Coverage {
	if d == nil || len(attempts) == 0 {
		return nil
	}
	pinned := pinnedRuns(d)
	at := decisionTime(*d)
	cov := &Coverage{
		Attempts:  []int{},
		Undecided: []int{},
		Basis:     coverageBasisTime,
		DecidedAt: at,
	}
	if len(pinned) > 0 {
		cov.Basis = coverageBasisRefs
	}
	for i := range attempts {
		if covers(attempts[i], pinned, at, d.Decision) {
			attempts[i].DecisionCovered = true
			cov.Attempts = append(cov.Attempts, attempts[i].Number)
			continue
		}
		cov.Undecided = append(cov.Undecided, attempts[i].Number)
	}
	sort.Ints(cov.Attempts)
	sort.Ints(cov.Undecided)
	cov.CoversNewest = coversCurrentState(attempts)
	return cov
}

// coversCurrentState reports whether the pin reaches the newest attempt that a
// verdict can be about. `attempts` is newest-first.
func coversCurrentState(attempts []Attempt) bool {
	for _, a := range attempts {
		if a.Rehearsal {
			continue
		}
		return a.DecisionCovered
	}
	// Every attempt was a rehearsal. Nothing here was produced, so nothing here
	// is governed — and the outcome endpoints refuse to record an acceptance on
	// it in the first place.
	return false
}

// AttemptRange reads a run of chapter numbers as prose: "1–2", "3, 5", "4".
// Empty in, empty out — a caller that has nothing to name should say nothing
// rather than print an empty range.
func AttemptRange(numbers []int) string {
	if len(numbers) == 0 {
		return ""
	}
	sorted := append([]int(nil), numbers...)
	sort.Ints(sorted)
	var parts []string
	start, prev := sorted[0], sorted[0]
	flush := func() {
		switch {
		case start == prev:
			parts = append(parts, strconv.Itoa(start))
		case prev == start+1:
			parts = append(parts, strconv.Itoa(start)+", "+strconv.Itoa(prev))
		default:
			parts = append(parts, strconv.Itoa(start)+"–"+strconv.Itoa(prev))
		}
	}
	for _, n := range sorted[1:] {
		if n == prev || n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return strings.Join(parts, ", ")
}
