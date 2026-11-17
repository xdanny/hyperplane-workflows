package journey

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

var base = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func run(id, dag, project, state string, offsetMin int, doc map[string]any) store.Run {
	at := base.Add(time.Duration(offsetMin) * time.Minute)
	r := store.Run{
		RunID:     id,
		DAG:       dag,
		Project:   project,
		State:     state,
		StartedAt: at,
		UpdatedAt: at.Add(time.Minute),
	}
	if Terminal(state) {
		r.EndedAt = at.Add(time.Minute)
	}
	if doc != nil {
		b, err := json.Marshal(doc)
		if err != nil {
			panic(err)
		}
		r.Doc = b
	}
	return r
}

func recipeDoc(recipe string) map[string]any { return map[string]any{"recipe": recipe} }
func findBucket(t *testing.T, buckets []Bucket, runID string) Bucket {
	t.Helper()
	for _, b := range buckets {
		for _, r := range b.Runs {
			if r.RunID == runID {
				return b
			}
		}
	}
	t.Fatalf("no bucket contains run %q", runID)
	return Bucket{}
}
func TestKeyIsDeterministicAndIdentityScoped(t *testing.T) {
	k := Key("acme", "ship-api", "recipe-x", "run-1")
	if k != Key("acme", "ship-api", "recipe-x", "run-1") {
		t.Fatal("key is not deterministic")
	}
	if !ValidKey(k) {
		t.Fatalf("Key produced a key ValidKey rejects: %q", k)
	}
	if k != Key("acme", "ship-api", "recipe-x", "some-other-root") {
		t.Fatal("recipe-keyed journeys must not depend on the chain root")
	}
	for name, other := range map[string]string{
		"project": Key("other", "ship-api", "recipe-x", "run-1"),
		"dag":     Key("acme", "other-dag", "recipe-x", "run-1"),
		"recipe":  Key("acme", "ship-api", "recipe-y", "run-1"),
	} {
		if other == k {
			t.Errorf("%s does not separate journeys", name)
		}
	}
	if Key("acme", "ship-api", "", "run-1") == Key("acme", "ship-api", "", "run-2") {
		t.Error("recipe-less runs with different chain roots must not share a journey")
	}
	if ValidKey("j-nothex0000000000") || ValidKey("run-1") || ValidKey("") {
		t.Error("ValidKey accepted a key that could not have been derived")
	}
}
func TestGroupJoinsFreshAttemptsOfSameRecipe(t *testing.T) {
	first := run("r1", "ship-api", "acme", "failed", 0, recipeDoc("rx"))
	second := run("r2", "ship-api", "acme", "failed", 30, recipeDoc("rx"))
	otherRecipe := run("r3", "ship-api", "acme", "failed", 40, recipeDoc("ry"))
	otherProject := run("r4", "ship-api", "globex", "failed", 50, recipeDoc("rx"))

	buckets := Group([]store.Run{otherProject, otherRecipe, second, first})
	if len(buckets) != 3 {
		t.Fatalf("got %d journeys, want 3 (rx@acme, ry@acme, rx@globex)", len(buckets))
	}
	rx := findBucket(t, buckets, "r1")
	if len(rx.Runs) != 2 {
		t.Fatalf("fresh attempts of the same recipe did not join: %d attempts", len(rx.Runs))
	}
	if rx.Runs[0].RunID != "r2" {
		t.Errorf("newest attempt = %q, want r2", rx.Runs[0].RunID)
	}
}
func TestGroupSeparatesRecipelessRuns(t *testing.T) {
	a := run("r1", "build-feature", "acme", "failed", 0, nil)
	b := run("r2", "build-feature", "acme", "failed", 30, nil)

	buckets := Group([]store.Run{b, a})
	if len(buckets) != 2 {
		t.Fatalf("got %d journeys, want 2 — unrelated ad-hoc runs are separate deliveries", len(buckets))
	}
}
func TestGroupSkipsSubRuns(t *testing.T) {
	parent := run("r1", "ship-api", "acme", "passed", 0, recipeDoc("rx"))
	child := run("r2", "trusted-proc", "acme", "passed", 1, nil)
	child.ParentRun = "r1"

	buckets := Group([]store.Run{child, parent})
	if len(buckets) != 1 {
		t.Fatalf("got %d journeys, want 1 — sub-runs are not deliveries", len(buckets))
	}
	if len(buckets[0].Runs) != 1 || buckets[0].Runs[0].RunID != "r1" {
		t.Errorf("bucket runs = %v, want only the top-level run", buckets[0].Runs)
	}
}
func TestRunOutcomeOnNewestAttemptSatisfiesJourney(t *testing.T) {
	first := run("r1", "ship-api", "acme", "failed", 0, recipeDoc("rx"))
	second := run("r2", "ship-api", "acme", "passed", 30, recipeDoc("rx"))
	bucket := Group([]store.Run{second, first})[0]

	onNewest := Evidence{RunOutcomes: map[string]store.Decision{
		store.OutcomeDecisionID("r2"): {ID: store.OutcomeDecisionID("r2"), Decision: "accepted", CreatedAt: base},
	}}
	j := Build(bucket, onNewest)
	if j.NeedsDecision {
		t.Error("a run outcome on the newest attempt should satisfy the journey")
	}
	if j.Stage != StageAccepted {
		t.Errorf("stage = %q, want accepted", j.Stage)
	}
	if j.Outcome == nil || j.Outcome.Scope != "run" || j.Outcome.RunID != "r2" {
		t.Errorf("effective outcome = %+v, want the run-grain one from r2", j.Outcome)
	}

	onSuperseded := Evidence{RunOutcomes: map[string]store.Decision{
		store.OutcomeDecisionID("r1"): {ID: store.OutcomeDecisionID("r1"), Decision: "rejected", CreatedAt: base},
	}}
	j = Build(bucket, onSuperseded)
	if !j.NeedsDecision {
		t.Error("a verdict on a superseded attempt must not answer for the delivery")
	}
	if j.Attempts[1].Outcome == nil {
		t.Error("the superseded attempt should still carry its own annotation")
	}
}
func TestNeedingDecisionIsOneCardPerJourney(t *testing.T) {
	var runs []store.Run
	for i, id := range []string{"r1", "r2", "r3", "r4"} {
		runs = append(runs, run(id, "ship-api", "acme", "failed", i*10, recipeDoc("rx")))
	}
	buckets := Group(runs)
	if len(buckets) != 1 {
		t.Fatalf("got %d journeys, want 1", len(buckets))
	}
	j := Build(buckets[0], Evidence{})
	pending := NeedingDecision([]Journey{j})
	if len(pending) != 1 {
		t.Fatalf("four attempts produced %d cards, want exactly 1", len(pending))
	}
	if pending[0].AttemptCount != 4 {
		t.Errorf("the one card lists %d attempts, want 4", pending[0].AttemptCount)
	}
	for i, a := range j.Attempts {
		if i > 0 && !a.Superseded {
			t.Errorf("attempt %s is not the newest but is not superseded", a.RunID)
		}
	}
	d := store.Decision{ID: "outcome-journey-x", Decision: "accepted", CreatedAt: base,
		Refs: []string{"journey:x", "run:r1", "run:r2", "run:r3", "run:r4"}}
	decided := Build(buckets[0], Evidence{JourneyOutcome: &d})
	if got := NeedingDecision([]Journey{decided}); len(got) != 0 {
		t.Errorf("a decided delivery still asks for %d decisions", len(got))
	}
}
func TestNameFallsBackThroughGoalRecipeDag(t *testing.T) {
	withGoal := run("r1", "ship-api", "acme", "failed", 0, recipeDoc("rx"))
	withGoal.Goal = "Ship the pricing API"
	if j := Build(Group([]store.Run{withGoal})[0], Evidence{}); j.Name != "Ship the pricing API" {
		t.Errorf("name = %q, want the goal", j.Name)
	}
	withRecipe := run("r2", "ship-api", "acme", "failed", 0, recipeDoc("rx"))
	if j := Build(Group([]store.Run{withRecipe})[0], Evidence{}); j.Name != "rx" {
		t.Errorf("name = %q, want the recipe", j.Name)
	}
	plain := run("r3", "ship-api", "acme", "failed", 0, nil)
	if j := Build(Group([]store.Run{plain})[0], Evidence{}); j.Name != "ship-api" {
		t.Errorf("name = %q, want the dag", j.Name)
	}
}
func TestNewestAttemptIsNeverSupersededWhateverItsMode(t *testing.T) {
	real := run("r1", "ship-api", "acme", "failed", 0, recipeDoc("rx"))
	rehearsal := run("r2", "ship-api", "acme", "passed", 30, recipeDoc("rx"))
	rehearsal.Mode = "plan"

	j := Build(Group([]store.Run{rehearsal, real})[0], Evidence{})
	if j.Attempts[0].RunID != "r2" {
		t.Fatalf("newest attempt = %q, want r2", j.Attempts[0].RunID)
	}
	if !j.Attempts[0].Rehearsal {
		t.Fatal("fixture broken: r2 should read as a rehearsal")
	}
	if j.Attempts[0].Superseded {
		t.Error("the newest attempt is the delivery's current attempt — a rehearsal is still an attempt")
	}
	if !j.Attempts[1].Superseded {
		t.Error("an attempt with a newer one after it is superseded, whatever mode the newer one ran in")
	}
}
func TestRehearsalFollowedByARealAttemptIsSuperseded(t *testing.T) {
	rehearsal := run("r1", "ship-api", "acme", "passed", 0, recipeDoc("rx"))
	rehearsal.Mode = "plan"
	real := run("r2", "ship-api", "acme", "failed", 30, recipeDoc("rx"))

	j := Build(Group([]store.Run{real, rehearsal})[0], Evidence{})
	if j.Attempts[0].Superseded {
		t.Error("the newest attempt must not be superseded")
	}
	if !j.Attempts[1].Superseded {
		t.Error("a rehearsal with a real attempt after it has been overtaken")
	}
}
func docWith(t *testing.T, doc store.JSON, key, value string) store.JSON {
	t.Helper()
	m := map[string]any{}
	if len(doc) > 0 {
		if err := json.Unmarshal(doc, &m); err != nil {
			t.Fatal(err)
		}
	}
	m[key] = value
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
