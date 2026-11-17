package journey

import (
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func journeyOf(t *testing.T, runs []store.Run, d *store.Decision) Journey {
	t.Helper()
	buckets := Group(runs)
	if len(buckets) != 1 {
		t.Fatalf("got %d deliveries, want 1", len(buckets))
	}
	return Build(buckets[0], Evidence{JourneyOutcome: d})
}
func TestAcceptanceCoversOnlyTheAttemptsItWasGivenOn(t *testing.T) {
	var runs []store.Run
	for i, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		runs = append(runs, run(id, "ship-api", "acme", "failed", i*10, recipeDoc("rx")))
	}
	decidedAt := base.Add(15 * time.Minute)
	d := store.Decision{
		ID: "outcome-journey-x", Decision: "accepted", CreatedAt: decidedAt, DecidedAt: &decidedAt,
		Refs: []string{"journey:x", "run:r1", "run:r2"},
	}
	j := journeyOf(t, runs, &d)

	if j.Coverage == nil {
		t.Fatal("a recorded verdict produced no coverage")
	}
	if j.Coverage.Basis != coverageBasisRefs {
		t.Errorf("basis = %q, want %q — the decision named its attempts", j.Coverage.Basis, coverageBasisRefs)
	}
	if got := AttemptRange(j.Coverage.Attempts); got != "1, 2" {
		t.Errorf("covered attempts read %q, want \"1, 2\"", got)
	}
	if got := AttemptRange(j.Coverage.Undecided); got != "3–5" {
		t.Errorf("undecided attempts read %q, want \"3–5\"", got)
	}
	if j.Coverage.CoversNewest {
		t.Error("a verdict recorded at attempt 2 claims to cover attempt 5")
	}
	if j.Outcome != nil {
		t.Errorf("outcome = %+v, want none — the delivery is undecided again", j.Outcome)
	}
	if j.RecordedOutcome == nil || j.RecordedOutcome.Verdict != "accepted" {
		t.Errorf("recordedOutcome = %+v, want the acceptance kept on record", j.RecordedOutcome)
	}
	if j.Stage != StageNeedsDecision {
		t.Errorf("stage = %q, want needs_decision", j.Stage)
	}
	if j.Maturity == MaturityAccepted {
		t.Error("maturity is accepted on attempts nobody accepted")
	}
	for _, a := range j.Attempts {
		want := a.Number <= 2
		if a.DecisionCovered != want {
			t.Errorf("attempt %d covered = %v, want %v", a.Number, a.DecisionCovered, want)
		}
	}
}
func TestAcceptanceStillGovernsWhenItNamesTheNewestAttempt(t *testing.T) {
	runs := []store.Run{
		run("r1", "ship-api", "acme", "failed", 0, recipeDoc("rx")),
		run("r2", "ship-api", "acme", "passed", 10, recipeDoc("rx")),
	}
	at := base.Add(30 * time.Minute)
	d := store.Decision{ID: "outcome-journey-x", Decision: "accepted", CreatedAt: at, DecidedAt: &at,
		Refs: []string{"journey:x", "run:r1", "run:r2"}}
	j := journeyOf(t, runs, &d)

	if j.Outcome == nil || j.Outcome.Verdict != "accepted" {
		t.Fatalf("outcome = %+v, want the acceptance governing", j.Outcome)
	}
	if !j.Coverage.CoversNewest || len(j.Coverage.Undecided) != 0 {
		t.Errorf("coverage = %+v, want every attempt covered", j.Coverage)
	}
	if j.Stage != StageAccepted || j.Maturity != MaturityAccepted {
		t.Errorf("stage/maturity = %q/%q, want accepted/accepted", j.Stage, j.Maturity)
	}
	if j.NeedsDecision {
		t.Error("a fully covered acceptance is still asking for a verdict")
	}
}
func TestRehearsalNeverInheritsAnAcceptance(t *testing.T) {
	real := run("r1", "ship-api", "acme", "passed", 0, recipeDoc("rx"))
	dry := run("r2", "ship-api", "acme", "failed", 10, recipeDoc("rx"))
	dry.Mode = "dry-run"

	at := base.Add(30 * time.Minute)
	d := store.Decision{ID: "outcome-journey-x", Decision: "accepted", CreatedAt: at, DecidedAt: &at,
		Refs: []string{"run:r1", "run:r2"}}
	j := journeyOf(t, []store.Run{real, dry}, &d)

	for _, a := range j.Attempts {
		if a.RunID == "r2" && a.DecisionCovered {
			t.Error("a rehearsal inherited an acceptance")
		}
		if a.RunID == "r1" && !a.DecisionCovered {
			t.Error("the real attempt lost the verdict it was given")
		}
	}
	if j.Outcome == nil || j.Outcome.Verdict != "accepted" {
		t.Errorf("outcome = %+v — the real attempt's acceptance still governs", j.Outcome)
	}
	if !j.Coverage.CoversNewest {
		t.Error("a rehearsal after a decided real attempt un-decided the delivery")
	}
	rej := store.Decision{ID: "outcome-journey-x", Decision: "rejected", CreatedAt: at, DecidedAt: &at,
		Refs: []string{"run:r1", "run:r2"}}
	jr := journeyOf(t, []store.Run{real, dry}, &rej)
	if jr.Outcome == nil || jr.Outcome.Verdict != "rejected" {
		t.Errorf("outcome = %+v, want the rejection to stand", jr.Outcome)
	}
}
func TestRefslessDecisionFallsBackToItsTimestamp(t *testing.T) {
	runs := []store.Run{
		run("r1", "ship-api", "acme", "failed", 0, recipeDoc("rx")),
		run("r2", "ship-api", "acme", "failed", 40, recipeDoc("rx")),
	}
	at := base.Add(20 * time.Minute)
	d := store.Decision{ID: "outcome-journey-x", Decision: "accepted", CreatedAt: at, DecidedAt: &at}
	j := journeyOf(t, runs, &d)

	if j.Coverage.Basis != coverageBasisTime {
		t.Errorf("basis = %q, want %q", j.Coverage.Basis, coverageBasisTime)
	}
	if got := AttemptRange(j.Coverage.Attempts); got != "1" {
		t.Errorf("covered = %q, want \"1\" — attempt 2 started after the verdict", got)
	}
	if j.Coverage.CoversNewest {
		t.Error("a verdict older than the newest attempt claims to cover it")
	}
}
func TestAttemptRangeReadsAsProse(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{4}, "4"},
		{[]int{1, 2}, "1, 2"},
		{[]int{3, 4, 5}, "3–5"},
		{[]int{5, 3, 4}, "3–5"},
		{[]int{1, 2, 5}, "1, 2, 5"},
		{[]int{1, 2, 3, 7, 8, 9}, "1–3, 7–9"},
	}
	for _, c := range cases {
		if got := AttemptRange(c.in); got != c.want {
			t.Errorf("AttemptRange(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
