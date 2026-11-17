package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBriefsAreInsertOnlyAndLatestWins pins the two properties every reader of
// this table depends on: a brief is never overwritten, and "the brief" means
// the newest one.
func TestBriefsAreInsertOnlyAndLatestWins(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	subject := "run-brief-store-" + uniq()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM briefs WHERE subject_id=$1`, subject)
	})

	first, err := st.InsertBrief(ctx, Brief{
		ID: subject + "-1", SubjectKind: BriefSubjectRun, SubjectID: subject,
		Body: `{"whatHappened":"first"}`, Grounded: true, Engine: BriefEngineFallback,
		InputsDigest: "aaa",
	})
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}

	// A taken id is refused rather than silently replacing an account.
	if _, err := st.InsertBrief(ctx, Brief{
		ID: first.ID, SubjectKind: BriefSubjectRun, SubjectID: subject,
		Body: `{"whatHappened":"an overwrite"}`, Engine: BriefEngineCodex, InputsDigest: "bbb",
	}); !errors.Is(err, ErrExists) {
		t.Fatalf("re-inserting the same id must be ErrExists, got %v", err)
	}

	second, err := st.InsertBrief(ctx, Brief{
		ID: subject + "-2", SubjectKind: BriefSubjectRun, SubjectID: subject,
		Body: `{"whatHappened":"second, after the repair"}`, Grounded: false,
		UngroundedNumbers: "9,412 0.3058", Engine: BriefEngineCodex, InputsDigest: "ccc",
	})
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}

	latest, err := st.LatestBrief(ctx, BriefSubjectRun, subject)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.ID != second.ID {
		t.Errorf("latest is %q, want the newest (%q)", latest.ID, second.ID)
	}
	// The flags survive the round trip — including a token that contains the
	// comma that used to be the separator.
	if latest.Grounded || latest.UngroundedNumbers != "9,412 0.3058" {
		t.Errorf("the honesty flags did not round-trip: grounded=%v ungrounded=%q",
			latest.Grounded, latest.UngroundedNumbers)
	}
	if _, err := st.LatestBrief(ctx, BriefSubjectRun, "nobody-briefed-this"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unbriefed subject must be ErrNotFound, got %v", err)
	}
}

// TestDeliveryDecisionForRunSpansBothGrains is the lookup the whole delivery
// half of briefs rests on: the inbox records verdicts against the DELIVERY, so
// a run-grain-only lookup answers "nobody decided" about most decided
// deliveries. A verdict recorded on the attempt itself is more specific and
// wins when both exist.
func TestDeliveryDecisionForRunSpansBothGrains(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	runID := "run-grain-" + uniq()
	journeyDecision := "journey-outcome-" + uniq()
	t.Cleanup(func() {
		_ = st.DeleteDecision(ctx, journeyDecision)
		_ = st.DeleteDecision(ctx, OutcomeDecisionID(runID))
	})

	if _, err := st.DeliveryDecisionForRun(ctx, runID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an undecided run must be ErrNotFound, got %v", err)
	}

	// Journey grain: recorded against the delivery, referencing this attempt.
	now := time.Now().UTC()
	if _, err := st.CreateDecision(ctx, Decision{
		ID: journeyDecision, Title: "Delivery", Decision: "accepted",
		Why: "shipped it", Tags: []string{OutcomeTag, "accepted", "journey"},
		Refs: []string{"journey:j-1", "run:" + runID}, DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create journey decision: %v", err)
	}
	got, err := st.DeliveryDecisionForRun(ctx, runID)
	if err != nil {
		t.Fatalf("journey-grain lookup: %v", err)
	}
	if got.ID != journeyDecision || got.Decision != "accepted" {
		t.Errorf("journey-grain verdict not found: %+v", got)
	}

	// Run grain: more specific, so it wins.
	if _, err := st.CreateDecision(ctx, Decision{
		ID: OutcomeDecisionID(runID), Title: "Outcome", Decision: "changes_requested",
		Tags: []string{OutcomeTag}, DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create run decision: %v", err)
	}
	got, err = st.DeliveryDecisionForRun(ctx, runID)
	if err != nil {
		t.Fatalf("run-grain lookup: %v", err)
	}
	if got.Decision != "changes_requested" {
		t.Errorf("a verdict recorded on this attempt must win, got %+v", got)
	}
}

// TestDeliveryDecisionForRunIsPinned is M-18 at the store boundary. The lookup
// spans both grains, so it is also the place a journey-grain acceptance could
// reach an attempt that verdict never saw.
//
// The pin needs no extra column: a journey decision names every attempt that
// existed when it was written, so an attempt started afterwards is simply not in
// its refs. What DOES need saying is the rehearsal rule — a dry run produced
// nothing, both outcome endpoints refuse to record an acceptance on one, and
// inheriting one through this lookup would walk straight around that refusal.
func TestDeliveryDecisionForRunIsPinned(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()
	covered := "run-covered-" + suffix
	later := "run-later-" + suffix
	dry := "run-dry-" + suffix
	decisionID := "journey-outcome-" + suffix
	t.Cleanup(func() { _ = st.DeleteDecision(ctx, decisionID) })

	for _, r := range []Run{
		{RunID: covered, DAG: "d", State: "failed", Project: "pin-" + suffix},
		{RunID: later, DAG: "d", State: "failed", Project: "pin-" + suffix},
		{RunID: dry, DAG: "d", State: "passed", Mode: "dry-run", Project: "pin-" + suffix},
	} {
		if err := st.CreateRun(ctx, r); err != nil {
			t.Fatalf("create run %s: %v", r.RunID, err)
		}
	}

	now := time.Now().UTC()
	// The verdict names the attempts that existed when it was recorded — the
	// covered one and the rehearsal, NOT the attempt that came later.
	if _, err := st.CreateDecision(ctx, Decision{
		ID: decisionID, Title: "Delivery", Decision: "accepted",
		Tags:      []string{OutcomeTag, "accepted", "journey"},
		Refs:      []string{"journey:j-pin", "run:" + covered, "run:" + dry},
		DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create journey decision: %v", err)
	}

	if got, err := st.DeliveryDecisionForRun(ctx, covered); err != nil || got.ID != decisionID {
		t.Errorf("the attempt the verdict was given on lost it: %+v %v", got, err)
	}
	if _, err := st.DeliveryDecisionForRun(ctx, later); !errors.Is(err, ErrNotFound) {
		t.Errorf("an attempt started after the verdict inherited it (err = %v)", err)
	}
	if _, err := st.DeliveryDecisionForRun(ctx, dry); !errors.Is(err, ErrNotFound) {
		t.Errorf("a REHEARSAL inherited an acceptance the endpoints refuse to record (err = %v)", err)
	}
}

// A later write stays current even if both wall time and the external ID move
// backwards. Project narratives use that same order after deduplicating subjects.
func TestBriefRecencyUsesInsertionOrderDespiteClockReversal(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	project := "brief-order-" + uniq()
	subjectA, subjectB := project+"-a", project+"-b"
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM briefs WHERE project=$1 OR project=$2`, project, project+"-other")
	})
	insert := func(id, subject, scope, kind string, year int) Brief {
		t.Helper()
		created := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		b, err := scanBrief(st.Pool.QueryRow(ctx, `
   INSERT INTO briefs(id,subject_kind,subject_id,project,body,grounded,engine,inputs_digest,created_at)
   VALUES($1,$2,$3,$4,'{}',true,'fallback','test',$5) RETURNING `+briefCols,
			id, kind, subject, scope, created))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := insert(project+"-z-first", subjectA, project, BriefSubjectDelivery, 2030)
	second := insert(project+"-m-other-subject", subjectB, project, BriefSubjectDelivery, 2020)
	newest := insert(project+"-a-revised", subjectA, project, BriefSubjectDelivery, 2010)
	if newest.ID >= first.ID || !newest.CreatedAt.Before(second.CreatedAt) {
		t.Fatal("fixture does not reverse ID and timestamp ordering")
	}
	// Newer unrelated rows must not change either lookup's subject/project scope.
	insert(project+"-other-project", project+"-c", project+"-other", BriefSubjectDelivery, 2000)
	insert(project+"-other-kind", subjectA, project, BriefSubjectRun, 2000)
	latest, err := st.LatestBrief(ctx, BriefSubjectDelivery, subjectA)
	if err != nil || latest.ID != newest.ID || !latest.CreatedAt.Equal(newest.CreatedAt) {
		t.Fatalf("latest used clock/ID order or changed display time: %+v (%v)", latest, err)
	}
	for _, limit := range []int{0, 1, 2} {
		got, err := st.ListBriefs(ctx, project, BriefSubjectDelivery, limit)
		want := []string{newest.ID, second.ID}
		if limit == 1 {
			want = want[:1]
		}
		if err != nil || len(got) != len(want) {
			t.Fatalf("limit %d: %+v (%v)", limit, got, err)
		}
		for i, id := range want {
			if got[i].ID != id {
				t.Fatalf("limit %d position %d: %s, want %s", limit, i, got[i].ID, id)
			}
		}
	}
}
