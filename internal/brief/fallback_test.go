package brief

import (
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// failedDoc is the state this package exists for: a run that stopped at a
// named step, proved some of what it promised, and nobody has ruled on yet.
func failedDoc() StateDoc {
	return StateDoc{
		Kind:      store.BriefSubjectRun,
		SubjectID: "r-1",
		RunID:     "r-1",
		DAG:       "pr/report",
		Recipe:    "data-quality-audit",
		Goal:      "audit the orders table",
		State:     "failed",
		Criteria: []Criterion{
			{ID: "c1", Proven: true, ProofNode: "rules-verify", ProofState: "passed"},
			{ID: "c2", ProofNode: "scorecard-verify", ProofState: "failed"},
			{ID: "c3"},
		},
		Failures: []Failure{
			{NodeID: "scorecard-verify", Kind: "gate", State: "failed",
				Error: "gate failed", Verdict: "two approved rules never ran"},
		},
		FailingNode: "scorecard-verify",
	}
}

// TestFallbackStatesTheOutcomeInItsFirstSentence: the deterministic brief is
// what a reader gets when the engine is off or broken, so the facts that matter
// have to be in it — the workflow, the state, the step it stopped at, the
// criteria tally, and whether anybody has decided.
func TestFallbackRendersTheStatusFacts(t *testing.T) {
	d := Fallback(failedDoc())

	for _, want := range []string{"data-quality-audit", "failed", "scorecard-verify", "1 of 3 requested checks/reviews", "awaiting decision"} {
		if !strings.Contains(d.WhatHappened, want) {
			t.Errorf("what-happened is missing %q:\n%s", want, d.WhatHappened)
		}
	}
	if !strings.HasPrefix(d.WhatHappened, "This run of data-quality-audit ended failed") {
		t.Errorf("the verdict must lead the first sentence, got %q", d.WhatHappened)
	}
	if !strings.Contains(d.TrueOutcome, "2 requested checks/reviews did not pass") {
		t.Errorf("the unproven criteria must survive into the outcome:\n%s", d.TrueOutcome)
	}
	if !strings.Contains(d.NextSteps, "Record a delivery decision") {
		t.Errorf("an undecided run's next step is the decision, got %q", d.NextSteps)
	}
	if d.Empty() {
		t.Fatal("a fallback brief must never be empty")
	}
}

// TestFallbackNeverClaimsVerificationThatDidNotHappen: a run with no gate at
// all must not read as "0 of 0 proven", which a reader takes for "nothing
// failed".
func TestFallbackSaysWhenNothingWasVerified(t *testing.T) {
	doc := failedDoc()
	doc.Criteria = nil
	d := Fallback(doc)
	if !strings.Contains(d.WhatHappened, "No extra check or model review is recorded") {
		t.Errorf("an unverified run must say so:\n%s", d.WhatHappened)
	}
	if strings.Contains(d.WhatHappened, "0 of 0") {
		t.Errorf("an empty tally reads as a pass; it must not be printed:\n%s", d.WhatHappened)
	}
}

// TestFallbackCarriesTheDeliveryVerdictAndNote: once a person has decided, the
// deterministic brief says what they decided and quotes their note.
func TestFallbackCarriesTheDeliveryVerdict(t *testing.T) {
	doc := failedDoc()
	doc.Kind = store.BriefSubjectDelivery
	doc.Delivery = &Delivery{Verdict: "changes_requested", Note: "the scorecard is missing two rules"}
	d := Fallback(doc)
	// In plain words, never as the database token: "The human's delivery
	// decision was changes_requested." is a sentence written for a schema, and
	// it reached a reader in a brief.
	if !strings.Contains(d.WhatHappened, "changes requested") ||
		!strings.Contains(d.WhatHappened, "missing two rules") {
		t.Errorf("the verdict and its note must both appear:\n%s", d.WhatHappened)
	}
	if strings.Contains(d.WhatHappened, "changes_requested") {
		t.Errorf("the raw enum token reached the prose:\n%s", d.WhatHappened)
	}
	if !strings.Contains(d.NextSteps, "note recorded") {
		t.Errorf("changes-requested points at the note, got %q", d.NextSteps)
	}
}

// TestFallbackNamesARehearsalAsOne: a dry run planned a graph. Reporting it as
// work delivered is the exact confusion briefs exist to end.
func TestFallbackNamesARehearsal(t *testing.T) {
	doc := failedDoc()
	doc.State = "passed"
	doc.Rehearsal = true
	for i := range doc.Criteria {
		doc.Criteria[i].Rehearsal = true
	}
	d := Fallback(doc)
	if !strings.Contains(d.WhatHappened, "0 of 3 checks/reviews provide evidence — 1 passed by stand-ins") {
		t.Errorf("a rehearsal must say so:\n%s", d.WhatHappened)
	}
	if strings.Contains(d.WhatHappened, "awaiting decision") {
		t.Errorf("a rehearsal cannot await a delivery decision:\n%s", d.WhatHappened)
	}
	if !strings.Contains(d.TrueOutcome, "Nothing was delivered") {
		t.Errorf("a rehearsal delivered nothing and must say so:\n%s", d.TrueOutcome)
	}
}

func TestFallbackStripsRecipeLeadAndDoesNotInventAFailingStep(t *testing.T) {
	doc := StateDoc{Kind: store.BriefSubjectRun, DAG: "x", State: "passed", RecipePurpose: "Recipe. Two dashboards disagree."}
	got := Fallback(doc)
	if strings.Contains(got.WhatItBuilds, "Recipe. Two") || !strings.Contains(got.WhatItBuilds, "Two dashboards disagree") {
		t.Errorf("recipe lead was not stripped: %q", got.WhatItBuilds)
	}
	if strings.Contains(got.TrueOutcome, "failing step") {
		t.Errorf("a passing run invented a failing step: %q", got.TrueOutcome)
	}
}

// TestFallbackIsGroundedInItsOwnStateDocument is the property that lets the
// platform record a fallback brief as grounded=true without checking: every
// figure it prints is one it derived from the document it was handed.
func TestFallbackIsGroundedInItsOwnState(t *testing.T) {
	doc := failedDoc()
	if got := Ungrounded(Fallback(doc).Text(), doc.Text()); len(got) != 0 {
		t.Errorf("the deterministic brief used numbers its own state document does not contain: %v", got)
	}
}

// TestParseDocRoundTrips: what is stored is what comes back, and a body that is
// not the JSON document is still readable rather than an error.
func TestParseDocRoundTrips(t *testing.T) {
	in := Doc{WhatItBuilds: "a", WhatHappened: "b", TrueOutcome: "c", NextSteps: "d"}
	out := ParseDoc(encodeDoc(in))
	if out.Text() != in.Text() {
		t.Errorf("round trip lost something: %+v", out)
	}
	if got := ParseDoc("just some prose"); got.WhatHappened != "just some prose" {
		t.Errorf("a non-JSON body must still read back as prose, got %+v", got)
	}
	if !ParseDoc("").Empty() {
		t.Error("an empty body must read as empty, not as prose")
	}
}

// TestStateDocNamesTheKindOfEveryStatus: the document the engine reads has to
// distinguish the four things called "passed" in this platform, because the
// brief is required to and can only do it from what it is given.
func TestStateDocDistinguishesTheStatusKinds(t *testing.T) {
	doc := failedDoc()
	doc.Delivery = &Delivery{Verdict: "accepted"}
	text := doc.Text()
	for _, want := range []string{
		"SDK EXECUTION STATE (completion is not a quality judgement)",
		"REQUESTED CHECKS AND MODEL REVIEWS",
		"DELIVERY DECISION",
		"NOT CHECKED (no passing result recorded)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("state document is missing %q", want)
		}
	}
}

// TestFallbackRefusesToAskForAVerdictOnARehearsal is Q-41b. The fallback brief's
// "what to do next" sat four screens above a block that refuses to record a
// delivery decision on a dry run, and told the reader to record one.
func TestFallbackRefusesToAskForAVerdictOnARehearsal(t *testing.T) {
	doc := StateDoc{
		Kind:      store.BriefSubjectRun,
		RunID:     "20260822-225648.342-dataset-shift-audit",
		State:     "failed",
		Rehearsal: true,
	}
	got := Fallback(doc)
	if got.NextSteps != RehearsalNextStep {
		t.Errorf("next steps = %q, want %q", got.NextSteps, RehearsalNextStep)
	}
	if strings.Contains(got.NextSteps, "Record a delivery decision") {
		t.Error("the brief still asks for a verdict the run page refuses to take")
	}

	// The state document the model writes from says the same thing, so the two
	// briefs cannot drift.
	if sd := doc.Text(); !strings.Contains(sd, RehearsalNextStep) {
		t.Error("the state doc does not carry the rehearsal's only honest next step")
	}

	// A real run is untouched: it still asks for the verdict it can receive.
	real := doc
	real.Rehearsal = false
	if !strings.Contains(Fallback(real).NextSteps, "Record a delivery decision") {
		t.Errorf("a real undecided run stopped asking for a verdict: %q", Fallback(real).NextSteps)
	}
}
