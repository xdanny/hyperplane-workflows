package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// testStore opens the configured Postgres, skipping when none is reachable.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed learning test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// countingCodex answers with a canned proposal and counts how often the engine
// was actually reached — which is what "extraction ran" means, and what the
// idempotency rule is really about: never billing twice for one acceptance.
func countingCodex(t *testing.T, skillID string, calls *atomic.Int32) knowledge.CodexFunc {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id":          skillID,
		"title":       "Competitive teardown",
		"domain":      "research",
		"instruction": "Fix the comparison axes before gathering anything, then fill the matrix and cite every cell.",
		"doc":         "# Competitive teardown\n\n1. Fix the axes.\n2. Fill the matrix.\n3. Cite everything.",
		"tags":        []string{"research", "teardown"},
		"confidence":  0.8,
		"rationale":   "The model review checked citations for each claim.",
	})
	if err != nil {
		t.Fatalf("marshal proposal: %v", err)
	}
	return func(_ context.Context, _ codex.Options) (codex.Result, error) {
		calls.Add(1)
		return codex.Result{OK: true, Text: string(payload), JSON: payload}, nil
	}
}

// seedRun inserts an accepted SDK run with committed task and review records.
func seedRun(t *testing.T, st *store.Store, _ string, runID, state string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateRun(ctx, store.Run{
		RunID: runID, DAG: "market-scan", Goal: "size the note-taking market", State: state,
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"gather","kind":"task","instruction":"Gather offerings"},{"id":"review","kind":"check","instruction":"Review citations"}]}}`),
		Doc:     store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"gather","phase":"passed","verification":"not_requested","findings":["three rivals"]},{"id":"review","phase":"passed","findings":["every claim is cited"]}]}}`),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		_ = st.DeleteRun(context.Background(), runID)
		_ = st.DeleteRecord(context.Background(), RecordType, runID)
	})

	// The hook only ever fires AFTER a verdict is written (see
	// internal/httpapi/journeys.go), and extraction now refuses a run nobody
	// decided on — so the decision is part of the fixture, not an extra.
	now := time.Now().UTC()
	decisionID := store.OutcomeDecisionID(runID)
	if _, err := st.CreateDecision(ctx, store.Decision{
		ID: decisionID, Title: "Outcome · " + runID, Status: "accepted",
		Decision: "accepted", Tags: []string{store.OutcomeTag, "accepted"},
		Refs: []string{"run:" + runID}, Source: "run:" + runID, DecidedAt: &now,
	}); err != nil {
		t.Fatalf("record acceptance on %s: %v", runID, err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), decisionID) })

}

// deliveryFor builds the journey projection an acceptance would carry.
func deliveryFor(key string, attempts ...store.Run) journey.Journey {
	j := journey.Journey{Key: key, Name: "Note-taking market scan", DAG: "market-scan"}
	for _, r := range attempts {
		j.Attempts = append(j.Attempts, journey.Attempt{RunID: r.RunID, State: r.State, DAG: r.DAG})
	}
	j.AttemptCount = len(j.Attempts)
	return j
}

// TestAcceptanceExtractsOncePerDelivery is the idempotency contract. An
// acceptance may cost exactly one billed extraction, however many times the
// hook is reached — a retried request, a re-accept after deleting the decision,
// or two callers racing.
func TestAcceptanceExtractsOncePerDelivery(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	runID := uniqueID("run-accepted")
	seedRun(t, st, runsDir, runID, "passed")
	skillID := uniqueID("competitive-teardown")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	var calls atomic.Int32
	key := uniqueID("j")
	cfg := Config{RunsDir: runsDir, Codex: countingCodex(t, skillID, &calls)}
	j := deliveryFor(key, store.Run{RunID: runID, State: "passed", DAG: "market-scan"})

	res, err := FromAcceptedDelivery(ctx, st, cfg, j)
	if err != nil {
		t.Fatalf("first acceptance: %v", err)
	}
	if res.Status != StatusDrafted || res.SkillID != skillID {
		t.Fatalf("first acceptance = %+v, want a drafted %s", res, skillID)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("engine called %d times, want 1", got)
	}

	// The draft is inert and carries where it came from — both the run and the
	// delivery, so the proposal card can say what it learned from.
	sk, err := st.GetSkill(ctx, skillID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if !store.IsDraftSkill(sk) {
		t.Errorf("an automatic extraction must land as a DRAFT, got %q", store.SkillStatus(sk))
	}
	var meta map[string]any
	if err := json.Unmarshal(sk.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta["sourceRun"] != runID {
		t.Errorf("sourceRun = %v, want %s", meta["sourceRun"], runID)
	}
	if meta["sourceJourney"] != key || meta["sourceDelivery"] != "Note-taking market scan" {
		t.Errorf("delivery lineage not stamped: %v", meta)
	}
	if meta["learnedFrom"] != "acceptance" {
		t.Errorf("learnedFrom = %v, want acceptance", meta["learnedFrom"])
	}

	// Second time through: refused, and the engine is never reached.
	if _, err := FromAcceptedDelivery(ctx, st, cfg, j); !errors.Is(err, ErrAlreadyLearned) {
		t.Fatalf("second acceptance: got %v, want ErrAlreadyLearned", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("engine called %d times across two acceptances, want 1", got)
	}
}

func TestAcceptanceDoesNotBorrowOlderSuccessfulAttempt(t *testing.T) {
	st := testStore(t)
	runsDir := t.TempDir()

	passed := uniqueID("run-passed")
	failed := uniqueID("run-failed")
	seedRun(t, st, runsDir, passed, "passed")
	seedRun(t, st, runsDir, failed, "failed")

	skillID := uniqueID("teardown")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })
	var calls atomic.Int32
	key := uniqueID("j")

	// Attempts arrive newest-first: the failure is newer than the pass.
	j := deliveryFor(key,
		store.Run{RunID: failed, State: "failed"},
		store.Run{RunID: passed, State: "passed"})

	res, err := FromAcceptedDelivery(context.Background(), st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, skillID, &calls),
	}, j)
	if err != nil {
		t.Fatalf("acceptance: %v", err)
	}
	if res.Status != StatusSkipped || calls.Load() != 0 {
		t.Fatalf("borrowed older success: %+v calls=%d", res, calls.Load())
	}
}

// A failed current attempt consumes no claim, so a later accepted completion can be considered.
func TestAcceptanceWithoutPassedAttemptLearnsNothing(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	failed := uniqueID("run-only-failed")
	seedRun(t, st, runsDir, failed, "failed")

	var calls atomic.Int32
	key := uniqueID("j")

	res, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, uniqueID("never"), &calls),
	}, deliveryFor(key, store.Run{RunID: failed, State: "failed"}))
	if err != nil {
		t.Fatalf("acceptance: %v", err)
	}
	if res.Status != StatusSkipped {
		t.Errorf("status = %q, want %q", res.Status, StatusSkipped)
	}
	if calls.Load() != 0 {
		t.Error("the engine was called for a delivery with nothing verified")
	}
	if _, err := st.GetRecord(ctx, RecordType, failed); !errors.Is(err, store.ErrNotFound) {
		t.Error("nothing to learn from must not claim the delivery — a later passing attempt deserves the chance")
	}
}

// TestDuplicateShortCircuitsToTheJournal: when the base already covers the
// method, the proposal is dropped silently into the run's journal. It never
// becomes an inbox card, because "approve this near-duplicate" is not a
// question anybody can usefully answer.
func TestDuplicateShortCircuitsToTheJournal(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	runID := uniqueID("run-dupe")
	seedRun(t, st, runsDir, runID, "passed")

	existingID := uniqueID("competitive-teardown")
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: existingID, Name: "Competitive teardown", Category: "research",
		Description: "Map rivals on shared axes.", Instruction: "Compare rivals feature by feature.",
	}); err != nil {
		t.Fatalf("seed existing skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), existingID) })

	proposedID := uniqueID("competitive-teardowns")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), proposedID) })
	var calls atomic.Int32
	key := uniqueID("j")

	res, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, proposedID, &calls),
	}, deliveryFor(key, store.Run{RunID: runID, State: "passed"}))
	if err != nil {
		t.Fatalf("acceptance: %v", err)
	}
	if res.Status != StatusDuplicate || res.SkillID != existingID {
		t.Fatalf("result = %+v, want a duplicate pointing at %s", res, existingID)
	}

	// No second skill: nothing new is waiting in the inbox.
	if _, err := st.GetSkill(ctx, proposedID); !errors.Is(err, store.ErrNotFound) {
		t.Error("a duplicate must not persist a near-identical draft")
	}
	drafts, err := st.ListLearnedDrafts(ctx, 50)
	if err != nil {
		t.Fatalf("list learned drafts: %v", err)
	}
	for _, d := range drafts {
		if d.ID == proposedID {
			t.Error("a duplicate must not become a proposal card")
		}
	}

	// The trail exists, in the journal, where it costs nobody a decision.
	events, err := st.ListEvents(ctx, runID, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Type == "learning."+StatusDuplicate {
			found = true
		}
	}
	if !found {
		t.Errorf("no learning.duplicate journal event on run %s, got %+v", runID, events)
	}
}

// TestExtractionFailureIsRecordedNotSwallowed: a failed extraction leaves a
// readable trail and does not pretend a skill was proposed.
func TestExtractionFailureIsRecordedNotSwallowed(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	runID := uniqueID("run-fail")
	seedRun(t, st, runsDir, runID, "passed")
	key := uniqueID("j")

	boom := errors.New("engine unavailable")
	res, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir,
		Extract: func(context.Context, *store.Store, knowledge.Extractor, string) (knowledge.ExtractResult, error) {
			return knowledge.ExtractResult{}, boom
		},
	}, deliveryFor(key, store.Run{RunID: runID, State: "passed"}))
	if err == nil {
		t.Fatal("a failed extraction must be reported to the caller")
	}
	if res.Status != StatusFailed {
		t.Errorf("status = %q, want %q", res.Status, StatusFailed)
	}
	rec, gerr := st.GetRecord(ctx, RecordType, runID)
	if gerr != nil {
		t.Fatalf("claim record: %v", gerr)
	}
	if rec.State != StatusFailed {
		t.Errorf("claim record state = %q, want %q", rec.State, StatusFailed)
	}
}

// TestSecondDeliverySupportsAnExistingDraft: when a second accepted delivery
// reaches a method that is still a DRAFT, it does not disappear into the
// dedupe short-circuit — it becomes the second run behind that draft, and the
// record says "supported" rather than "duplicate". One passed run is an
// anecdote; this is how a draft stops being one.
func TestSecondDeliverySupportsAnExistingDraft(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	firstRun := uniqueID("run-support-a")
	secondRun := uniqueID("run-support-b")
	seedRun(t, st, runsDir, firstRun, "passed")
	seedRun(t, st, runsDir, secondRun, "passed")

	skillID := uniqueID("competitive-teardown")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })
	secondID := uniqueID("competitive-teardowns")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), secondID) })

	var calls atomic.Int32
	firstKey := uniqueID("j")
	first, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, skillID, &calls),
	}, deliveryFor(firstKey, store.Run{RunID: firstRun, State: "passed"}))
	if err != nil {
		t.Fatalf("first acceptance: %v", err)
	}
	if first.Status != StatusDrafted || first.SupportRuns != 1 {
		t.Fatalf("first acceptance = %+v, want a draft resting on 1 run", first)
	}

	secondKey := uniqueID("j")
	second, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, secondID, &calls),
	}, deliveryFor(secondKey, store.Run{RunID: secondRun, State: "passed"}))
	if err != nil {
		t.Fatalf("second acceptance: %v", err)
	}
	if second.Status != StatusSupported {
		t.Fatalf("second acceptance status = %q, want %q (%+v)", second.Status, StatusSupported, second)
	}
	if second.SkillID != skillID || second.SupportRuns != 2 {
		t.Errorf("second acceptance = %+v, want draft %s resting on 2 runs", second, skillID)
	}

	rec, err := st.GetRecord(ctx, RecordType, secondRun)
	if err != nil {
		t.Fatalf("claim record: %v", err)
	}
	if rec.State != StatusSupported {
		t.Errorf("claim record state = %q, want %q", rec.State, StatusSupported)
	}

	// Still one card in the inbox, now standing on two runs.
	drafts, err := st.ListLearnedDrafts(ctx, 50)
	if err != nil {
		t.Fatalf("list learned drafts: %v", err)
	}
	seen := 0
	for _, d := range drafts {
		if d.ID == secondID {
			t.Error("supporting a draft must not also create a second proposal card")
		}
		if d.ID != skillID {
			continue
		}
		seen++
		var meta struct {
			SourceRuns  []string `json:"sourceRuns"`
			SupportRuns int      `json:"supportRuns"`
		}
		if err := json.Unmarshal(d.Metadata, &meta); err != nil {
			t.Fatalf("draft metadata: %v", err)
		}
		if meta.SupportRuns != 2 || len(meta.SourceRuns) != 2 {
			t.Errorf("the card should say it rests on 2 runs, got %+v", meta)
		}
	}
	if seen != 1 {
		t.Errorf("expected exactly one proposal card for %s, saw %d", skillID, seen)
	}
}

// TestAcceptanceNeverLearnsFromARehearsal is D-8 at the grain the defect
// happened at: the automatic hook.
//
// Draft `multi-perspective-concept-validation` was extracted from run
// 20260820-160329.027-market-research — `mode: plan`, outcome `null` — and the
// inbox card read "Learned from a passed run you accepted". A rehearsal's agent
// nodes answer with stand-ins, so the "method that worked" names no work. The
// hook must spend no engine call and file no draft.
func TestAcceptanceNeverLearnsFromARehearsal(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	runID := uniqueID("run-rehearsed")
	seedRun(t, st, runsDir, runID, "passed")
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET mode='plan' WHERE run_id=$1`, runID); err != nil {
		t.Fatalf("mark run as a rehearsal: %v", err)
	}

	skillID := uniqueID("never-learned")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })
	var calls atomic.Int32
	key := uniqueID("j")

	res, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, skillID, &calls),
	}, deliveryFor(key, store.Run{RunID: runID, State: "passed"}))

	if err == nil {
		t.Fatal("learning from a rehearsal must fail loudly, not quietly draft something")
	}
	if !strings.Contains(err.Error(), "rehearsal") {
		t.Errorf("the failure must name the reason: %v", err)
	}
	if res.Status != StatusFailed {
		t.Errorf("status = %q, want %q", res.Status, StatusFailed)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("a rehearsal must never reach the engine; it made %d call(s)", got)
	}
	if _, gerr := st.GetSkill(ctx, skillID); gerr == nil {
		t.Error("a rehearsal must leave no draft skill behind")
	}
}

func TestConcurrentAcceptanceClaimsOnlyOneExtraction(t *testing.T) {
	st := testStore(t)
	runID := uniqueID("run-racing")
	seedRun(t, st, "", runID, "passed")
	j := deliveryFor(uniqueID("j"), store.Run{RunID: runID, State: "passed"})
	var calls atomic.Int32
	start := make(chan struct{})
	results := make(chan error, 2)
	cfg := Config{Extract: func(context.Context, *store.Store, knowledge.Extractor, string) (knowledge.ExtractResult, error) {
		calls.Add(1)
		return knowledge.ExtractResult{Status: knowledge.ResultDrafted, Skill: &store.Skill{ID: "proposed"}}, nil
	}}
	for i := 0; i < 2; i++ {
		go func() { <-start; _, err := FromAcceptedDelivery(context.Background(), st, cfg, j); results <- err }()
	}
	close(start)
	var successes, duplicates int
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err == nil {
				successes++
			} else if errors.Is(err, ErrAlreadyLearned) {
				duplicates++
			} else {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent claim did not finish")
		}
	}
	if successes != 1 || duplicates != 1 || calls.Load() != 1 {
		t.Fatalf("successes=%d duplicate=%d extraction calls=%d", successes, duplicates, calls.Load())
	}
	rec, err := st.GetRecord(t.Context(), RecordType, runID)
	if err != nil || rec.RunID != runID || rec.State != StatusDrafted {
		t.Fatalf("attempt claim not finalized: %+v %v", rec, err)
	}
}

func TestNewAcceptedAttemptCanSupportDraftWithoutBorrowingEarlierAcceptance(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()
	firstRun, secondRun := uniqueID("run-first"), uniqueID("run-next")
	seedRun(t, st, "", firstRun, "passed")
	seedRun(t, st, "", secondRun, "passed")
	secondDecision, err := st.GetDecision(ctx, store.OutcomeDecisionID(secondRun))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDecision(ctx, secondDecision.ID); err != nil {
		t.Fatal(err)
	}
	key := uniqueID("j")
	firstSkill, secondSkill := uniqueID("shared-method"), uniqueID("shared-method-again")
	t.Cleanup(func() {
		_ = st.DeleteSkill(context.Background(), firstSkill)
		_ = st.DeleteSkill(context.Background(), secondSkill)
	})
	var calls atomic.Int32
	first, err := FromAcceptedDelivery(ctx, st, Config{Codex: countingCodex(t, firstSkill, &calls)}, deliveryFor(key, store.Run{RunID: firstRun, State: "passed"}))
	if err != nil || first.Status != StatusDrafted {
		t.Fatalf("first learning: %+v %v", first, err)
	}
	j := deliveryFor(key, store.Run{RunID: secondRun, State: "passed"}, store.Run{RunID: firstRun, State: "passed"})
	cfg := Config{Codex: countingCodex(t, secondSkill, &calls)}
	before, err := FromAcceptedDelivery(ctx, st, cfg, j)
	if err != nil || before.Status != StatusSkipped || calls.Load() != 1 {
		t.Fatalf("borrowed old acceptance: %+v %v calls=%d", before, err, calls.Load())
	}
	if _, err := st.GetRecord(ctx, RecordType, secondRun); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unaccepted attempt consumed claim: %v", err)
	}
	if _, err := st.CreateDecision(ctx, secondDecision); err != nil {
		t.Fatal(err)
	}
	next, err := FromAcceptedDelivery(ctx, st, cfg, j)
	if err != nil || next.Status != StatusSupported || next.SkillID != firstSkill || next.SupportRuns != 2 || calls.Load() != 2 {
		t.Fatalf("new accepted attempt not considered: %+v %v calls=%d", next, err, calls.Load())
	}
	if next.DecisionID != secondDecision.ID {
		t.Fatalf("wrong decision provenance: %+v", next)
	}
	if _, err := FromAcceptedDelivery(ctx, st, cfg, j); !errors.Is(err, ErrAlreadyLearned) || calls.Load() != 2 {
		t.Fatalf("same attempt learned twice: %v calls=%d", err, calls.Load())
	}
}

func TestContinuedAttemptNeedsItsOwnAcceptanceBeforeAutomaticLearning(t *testing.T) {
	st := testStore(t)
	source, child := uniqueID("interrupted"), uniqueID("continued")
	seedRun(t, st, "", source, "interrupted")
	seedRun(t, st, "", child, "passed")
	decision, err := st.GetDecision(t.Context(), store.OutcomeDecisionID(child))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDecision(t.Context(), decision.ID); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]string{"sourceRunId": source, "rootRunId": source})
	if _, err := st.Pool.Exec(t.Context(), `UPDATE runs SET parent_run=$2,options=jsonb_set(options,'{continuation}',$3::jsonb) WHERE run_id=$1`, child, source, metadata); err != nil {
		t.Fatal(err)
	}
	j := deliveryFor(uniqueID("j"), store.Run{RunID: child, State: "passed"}, store.Run{RunID: source, State: "interrupted"})
	var calls atomic.Int32
	cfg := Config{Extract: func(_ context.Context, _ *store.Store, ex knowledge.Extractor, runID string) (knowledge.ExtractResult, error) {
		calls.Add(1)
		if runID != child || ex.Meta["sourceDecision"] != decision.ID {
			t.Errorf("wrong attempt/acceptance: run=%s metadata=%v", runID, ex.Meta)
		}
		return knowledge.ExtractResult{Status: knowledge.ResultDrafted, Skill: &store.Skill{ID: "continued-method"}}, nil
	}}
	result, err := FromAcceptedDelivery(t.Context(), st, cfg, j)
	if err != nil || result.Status != StatusSkipped || calls.Load() != 0 {
		t.Fatalf("borrowed source decision: %+v %v calls=%d", result, err, calls.Load())
	}
	if _, err := st.CreateDecision(t.Context(), decision); err != nil {
		t.Fatal(err)
	}
	result, err = FromAcceptedDelivery(t.Context(), st, cfg, j)
	if err != nil || result.RunID != child || result.Status != StatusDrafted || calls.Load() != 1 {
		t.Fatalf("accepted continuation not considered: %+v %v calls=%d", result, err, calls.Load())
	}
}

// An acceptance the server recorded itself, under a workflow's sign-off policy,
// is a restatement of check results — not a person saying the method was worth
// keeping. Extraction exists to learn from human judgment, so it must skip one,
// quietly and without billing an engine call.
func TestAutomaticAcceptanceTeachesNothing(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	runsDir := t.TempDir()

	runID := uniqueID("run-auto-accepted")
	seedRun(t, st, runsDir, runID, "passed")
	// seedRun records a person's acceptance; this delivery was accepted by the
	// platform instead.
	if _, err := st.Pool.Exec(ctx, `UPDATE decisions SET decided_by=$2 WHERE id=$1`,
		store.OutcomeDecisionID(runID), store.DecidedByAuto); err != nil {
		t.Fatalf("mark the acceptance automatic: %v", err)
	}

	skillID := uniqueID("never-learned-auto")
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })
	var calls atomic.Int32

	res, err := FromAcceptedDelivery(ctx, st, Config{
		RunsDir: runsDir, Codex: countingCodex(t, skillID, &calls),
	}, deliveryFor(uniqueID("j"), store.Run{RunID: runID, State: "passed"}))
	if err != nil {
		t.Fatalf("skipping is not an error: %v", err)
	}
	if res.Status != StatusSkipped {
		t.Errorf("status = %q, want %q", res.Status, StatusSkipped)
	}
	if !strings.Contains(res.Message, "not by a person") {
		t.Errorf("the message must say why it was skipped: %q", res.Message)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("an automatic acceptance must never reach the engine; it made %d call(s)", got)
	}
	if _, gerr := st.GetSkill(ctx, skillID); gerr == nil {
		t.Error("an automatic acceptance must leave no draft skill behind")
	}
}
