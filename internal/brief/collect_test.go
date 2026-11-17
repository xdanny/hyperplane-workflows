package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// briefStore opens the test database, skipping when none is reachable so
// `go test ./...` stays green offline.
func briefStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("cannot connect to database: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// seedAudit records a current SDK workflow: ordinary work, one passing program
// check, one failed model review and one review that has not run.
func seedAudit(t *testing.T, st *store.Store, _ string) string {
	t.Helper()
	runID := fmt.Sprintf("run-brief-%d", time.Now().UnixNano())
	err := st.CreateRun(context.Background(), store.Run{
		RunID: runID, DAG: "sdk-nodes", Goal: "audit the orders table", State: "failed", Project: "hyperplane", Mode: "run",
		StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(),
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[
   {"id":"profile","kind":"task","title":"Prepare profile"},
   {"id":"rules-check","kind":"task","title":"Check rules","check":["sh","checks.sh"]},
   {"id":"scorecard-review","kind":"check","title":"Review rule coverage"},
   {"id":"final-review","kind":"check","title":"Final review"}]}}`),
		Doc: store.JSON(`{"recipe":"data-quality-audit","slots":{"dataset":"orders"},"sdk":{"phase":"failed","currentNode":"scorecard-review","nodes":[
   {"id":"profile","phase":"passed","verification":"not_requested"},
   {"id":"rules-check","phase":"passed","verification":"passed"},
   {"id":"scorecard-review","phase":"failed","findings":["two approved rules never ran; observed 0.3058 against tolerance 0.25"]}]}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.DeleteDecision(context.Background(), store.OutcomeDecisionID(runID))
		_ = st.DeleteRun(context.Background(), runID)
	})
	return runID
}

// TestCollectAssemblesTheAuthoritativeState covers SDK check results and human outcomes.
func TestCollectAssemblesTheAuthoritativeState(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	runID := seedAudit(t, st, runsDir)

	doc, err := Collect(context.Background(), st, runID, store.BriefSubjectRun)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if doc.Recipe != "data-quality-audit" {
		t.Errorf("recipe not read off the run doc: %q", doc.Recipe)
	}
	if doc.State != "failed" || doc.FailingNode != "scorecard-review" {
		t.Errorf("run state not collected: state=%q failing=%q (want the current SDK review)", doc.State, doc.FailingNode)
	}
	total, proven := doc.ProvenCount()
	if total != 3 || proven != 1 {
		t.Errorf("criteria tally = %d/%d, want 1/3", proven, total)
	}

	text := doc.Text()
	for _, want := range []string{"two approved rules never ran", "0.3058", "tolerance 0.25", "NOT PASSED", "orders", "program_check", "model_review", "Artifact contents are not independently inspected"} {
		if !strings.Contains(text, want) {
			t.Errorf("the state document is missing %q", want)
		}
	}
	if doc.Delivery != nil {
		t.Errorf("no decision has been recorded, so the document must not claim one: %+v", doc.Delivery)
	}

	// Once a human decides, the same collector carries the verdict and the note.
	now := time.Now().UTC()
	if _, err := st.CreateDecision(context.Background(), store.Decision{
		ID: store.OutcomeDecisionID(runID), Title: "Outcome", Decision: "changes_requested",
		Why: "the scorecard is missing two rules", Tags: []string{store.OutcomeTag},
		Project: "hyperplane", DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	decided, err := Collect(context.Background(), st, runID, store.BriefSubjectDelivery)
	if err != nil {
		t.Fatalf("collect after decision: %v", err)
	}
	if decided.Delivery == nil || decided.Delivery.Verdict != "changes_requested" {
		t.Fatalf("the recorded verdict did not reach the state document: %+v", decided.Delivery)
	}
	if !strings.Contains(decided.Text(), "missing two rules") {
		t.Error("the person's own note must be carried verbatim")
	}
}

// fakeEngine returns a CodexFunc that answers with the given briefs in order,
// and records the prompts it was given.
func fakeEngine(prompts *[]string, answers ...any) CodexFunc {
	i := 0
	return func(_ context.Context, o codex.Options) (codex.Result, error) {
		*prompts = append(*prompts, o.Instruction)
		if o.SandboxMode != codex.SandboxReadOnly {
			return codex.Result{}, fmt.Errorf("a brief must run read-only, got %q", o.SandboxMode)
		}
		var a any
		if i < len(answers) {
			a = answers[i]
		} else {
			a = answers[len(answers)-1]
		}
		i++
		switch v := a.(type) {
		case error:
			return codex.Result{}, v
		case Doc:
			b, _ := json.Marshal(v)
			return codex.Result{OK: true, JSON: b}, nil
		}
		return codex.Result{}, errors.New("fakeEngine: unusable answer")
	}
}

// TestWriteRecordsAGroundedBrief: the happy path end to end, through the store.
func TestWriteRecordsAGroundedBrief(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	runID := seedAudit(t, st, runsDir)
	ctx := context.Background()

	doc, err := Collect(ctx, st, runID, store.BriefSubjectRun)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var prompts []string
	row, err := Write(ctx, st, Config{Enabled: true, RunsDir: runsDir, Codex: fakeEngine(&prompts, Doc{
		WhatItBuilds: "A data quality audit of the orders table.",
		WhatHappened: "The run stopped at the scorecard review: 1 of 3 requested checks/reviews passed.",
		TrueOutcome:  "You have a scorecard that is missing two approved rules.",
		NextSteps:    "Re-run the two rules that never executed.",
	})}, doc)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, row.ID) })

	if row.Engine != store.BriefEngineCodex || !row.Grounded {
		t.Errorf("expected a grounded engine brief, got engine=%q grounded=%v (%s)", row.Engine, row.Grounded, row.UngroundedNumbers)
	}
	if len(prompts) != 1 {
		t.Fatalf("a grounded first draft must not be retried; got %d call(s)", len(prompts))
	}
	if !strings.Contains(prompts[0], "0.3058") || !strings.Contains(prompts[0], "human-translation") &&
		!strings.Contains(prompts[0], "translate machine state") {
		t.Error("the prompt must carry both the state document and the writing standard")
	}
	if got := ParseDoc(row.Body); !strings.Contains(got.TrueOutcome, "missing two approved rules") {
		t.Errorf("the stored body lost its parts: %+v", got)
	}

	// Latest-wins, history kept: a second brief is a second row.
	second, err := Write(ctx, st, Config{Enabled: false}, doc)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, second.ID) })
	latest, err := st.LatestBrief(ctx, store.BriefSubjectRun, runID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.ID != second.ID {
		t.Errorf("latest brief is %q, want the newest (%q)", latest.ID, second.ID)
	}
	if latest.Engine != store.BriefEngineFallback {
		t.Errorf("with the engine switched off the platform must write the brief itself, got %q", latest.Engine)
	}
}

// TestWriteRetriesOnceThenFlagsTheNumbers is the honesty contract: an invented
// figure buys one retry with the offender quoted, and if it survives that, the
// brief is STORED and flagged rather than dropped or silently shipped.
func TestWriteRetriesOnceThenFlagsTheNumbers(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	runID := seedAudit(t, st, runsDir)
	ctx := context.Background()

	doc, err := Collect(ctx, st, runID, store.BriefSubjectRun)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	invented := Doc{
		WhatItBuilds: "A data quality audit.",
		WhatHappened: "The review failed after checking 9,412 rows.",
		TrueOutcome:  "A scorecard with gaps.",
		NextSteps:    "Re-run the missing rules.",
	}
	var prompts []string
	row, err := Write(ctx, st, Config{Enabled: true, RunsDir: runsDir,
		Codex: fakeEngine(&prompts, invented, invented)}, doc)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, row.ID) })

	if len(prompts) != 2 {
		t.Fatalf("an ungrounded draft must be retried exactly once; got %d call(s)", len(prompts))
	}
	if !strings.Contains(prompts[1], "9,412") {
		t.Error("the retry must quote the offending number back")
	}
	if row.Grounded {
		t.Error("a brief with an invented figure must not be recorded as grounded")
	}
	if !strings.Contains(row.UngroundedNumbers, "9,412") {
		t.Errorf("the offender must be named on the row, got %q", row.UngroundedNumbers)
	}
	if row.Engine != store.BriefEngineCodex {
		t.Errorf("the flagged brief is still the engine's, got %q", row.Engine)
	}
	if ParseDoc(row.Body).Empty() {
		t.Error("a flagged brief is still shown — it must not be stored empty")
	}
}

// TestWriteFallsBackWhenTheEngineFails: two failures and the platform writes
// the brief itself. There is no path that leaves an outcome unaccounted for.
func TestWriteFallsBackWhenTheEngineFails(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	runID := seedAudit(t, st, runsDir)
	ctx := context.Background()

	doc, err := Collect(ctx, st, runID, store.BriefSubjectRun)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var prompts []string
	row, err := Write(ctx, st, Config{Enabled: true, RunsDir: runsDir,
		Codex: fakeEngine(&prompts, errors.New("codex is not on PATH"))}, doc)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, row.ID) })

	if row.Engine != store.BriefEngineFallback {
		t.Errorf("engine = %q, want the deterministic fallback", row.Engine)
	}
	if !row.Grounded {
		t.Error("the deterministic brief is derived from the state document, so it is grounded by construction")
	}
	if body := ParseDoc(row.Body); !strings.Contains(body.WhatHappened, "1 of 3 requested checks/reviews") {
		t.Errorf("the fallback must still carry the status facts: %+v", body)
	}
}

// TestWriteNeverBillsTheEngineForARehearsal: a rehearsal executed no work. There
// is no outcome to translate, and paying for a sentence saying so is waste.
func TestWriteNeverBillsTheEngineForARehearsal(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	runID := seedAudit(t, st, runsDir)
	ctx := context.Background()

	// Straight SQL: UpdateRun rewrites every column, and this test needs the
	// seeded doc (its criteria) left exactly as it is.
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET mode='plan', state='passed' WHERE run_id=$1`, runID); err != nil {
		t.Fatalf("mark run as a rehearsal: %v", err)
	}
	doc, err := Collect(ctx, st, runID, store.BriefSubjectRun)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !doc.Rehearsal {
		t.Fatal("a run in plan mode must be collected as a rehearsal")
	}
	var prompts []string
	row, err := Write(ctx, st, Config{Enabled: true, RunsDir: runsDir,
		Codex: fakeEngine(&prompts, Doc{WhatHappened: "should never be asked for"})}, doc)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, row.ID) })

	if len(prompts) != 0 {
		t.Errorf("a rehearsal must never reach the engine; it made %d call(s)", len(prompts))
	}
	if row.Engine != store.BriefEngineFallback {
		t.Errorf("engine = %q, want the deterministic fallback", row.Engine)
	}
}

// TestWriteProjectNarrative: accepting a delivery is what changes "what this
// project has built so far", and the narrative lands on projects.narrative with
// the brief that produced it still on record.
func TestWriteProjectNarrative(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	ctx := context.Background()
	project := fmt.Sprintf("brief-proj-%d", time.Now().UnixNano())

	runID := seedAudit(t, st, runsDir)
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET state='passed', project=$2 WHERE run_id=$1`, runID, project); err != nil {
		t.Fatalf("scope the run to the project: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateDecision(ctx, store.Decision{
		ID: store.OutcomeDecisionID(runID), Title: "Outcome", Decision: "accepted",
		Tags: []string{store.OutcomeTag}, Project: project, DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project)
	})

	doc, err := Collect(ctx, st, runID, store.BriefSubjectDelivery)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	doc.Project = project
	delivery, err := Write(ctx, st, Config{Enabled: false}, doc)
	if err != nil {
		t.Fatalf("write delivery brief: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, delivery.ID) })

	var prompts []string
	narrative, err := WriteProject(ctx, st, Config{Enabled: true, Codex: fakeEngine(&prompts, Doc{
		WhatItBuilds: "This project has delivered one data quality audit of the orders table.",
		WhatHappened: "One delivery has been accepted.",
		TrueOutcome:  "An accepted scorecard for the orders table.",
		NextSteps:    "Audit the next dataset.",
	})}, project)
	if err != nil {
		t.Fatalf("write project narrative: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, narrative.ID) })

	if len(prompts) != 1 {
		t.Fatalf("the narrative should cost exactly one call, got %d", len(prompts))
	}
	if !strings.Contains(prompts[0], "ACCEPTED DELIVERIES") {
		t.Error("the narrative is written from the accepted deliveries' own briefs")
	}
	// Q-17's dialect half: the header asks what the project BUILT, and the
	// answer came back hedged ("it was intended to compare…") about a delivery
	// a human had accepted. The clause that forbids it must reach the prompt.
	for _, want := range []string{
		"AN ACCEPTED DELIVERY HAPPENED",
		"never \"it was intended to compare\"",
		"THE CLOSED LINES ARE PART OF THE STORY",
	} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("the project-narrative prompt does not carry: %q", want)
		}
	}
	p, err := st.GetProject(ctx, project)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if !strings.Contains(p.Narrative, "one data quality audit") {
		t.Errorf("projects.narrative was not written from the narrative, got %q", p.Narrative)
	}
}

// TestWriteProjectNeverTouchesSummary is the regression test for the bug that
// made this column split necessary.
//
// The project-narrative writer wrote into `projects.summary` — the human's
// "what this project is for" field. On a project whose owner had typed a
// description, accepting a delivery destroyed it; on a project with zero runs,
// the owner's typed intent rendered under a heading reading "WHAT THIS PROJECT
// HAS BUILT SO FAR", which was simply false. The writer must now be incapable
// of touching that field, whatever it is asked to write.
func TestWriteProjectNeverTouchesSummary(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	ctx := context.Background()
	project := fmt.Sprintf("brief-keepsummary-%d", time.Now().UnixNano())

	runID := seedAudit(t, st, runsDir)
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET state='passed', project=$2 WHERE run_id=$1`, runID, project); err != nil {
		t.Fatalf("scope the run to the project: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateDecision(ctx, store.Decision{
		ID: store.OutcomeDecisionID(runID), Title: "Outcome", Decision: "accepted",
		Tags: []string{store.OutcomeTag}, Project: project, DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project)
	})

	// The owner's own words, and a declaration document alongside them.
	if err := st.EnsureProject(ctx, project); err != nil {
		t.Fatalf("ensure project: %v", err)
	}
	mine := "Monthly reliability marts over service_reliability_events.csv for the SRE team."
	decl := store.JSON(`{"datasets":[{"name":"orders","location":"data/orders.csv"}]}`)
	if _, err := st.UpdateProject(ctx, project, store.ProjectPatch{
		Summary: &mine, Declarations: &decl,
	}); err != nil {
		t.Fatalf("write the owner's description: %v", err)
	}

	doc, err := Collect(ctx, st, runID, store.BriefSubjectDelivery)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	doc.Project = project
	delivery, err := Write(ctx, st, Config{Enabled: false}, doc)
	if err != nil {
		t.Fatalf("write delivery brief: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, delivery.ID) })

	var prompts []string
	row, err := WriteProject(ctx, st, Config{Enabled: true, Codex: fakeEngine(&prompts, Doc{
		WhatItBuilds: "This project has delivered one data quality audit of the orders table.",
		WhatHappened: "One delivery has been accepted.",
		TrueOutcome:  "An accepted scorecard for the orders table.",
		NextSteps:    "Audit the next dataset.",
	})}, project)
	if err != nil {
		t.Fatalf("write project narrative: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, row.ID) })

	p, err := st.GetProject(ctx, project)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.Summary != mine {
		t.Errorf("the narrative writer overwrote the owner's description:\n got  %q\n want %q", p.Summary, mine)
	}
	if !strings.Contains(p.Narrative, "one data quality audit") {
		t.Errorf("the narrative did not land in projects.narrative, got %q", p.Narrative)
	}
	if !strings.Contains(string(p.Declarations), "orders") {
		t.Errorf("the narrative write disturbed the declaration document: %s", p.Declarations)
	}
}

// TestProjectWithNoAcceptedDeliveryHasNoNarrative: the empty narrative IS the
// statement that nothing has been accepted. Nothing may be written into it —
// least of all the owner's description, which is what used to appear under the
// "has built so far" heading on a project with zero runs.
func TestProjectWithNoAcceptedDeliveryHasNoNarrative(t *testing.T) {
	st := briefStore(t)
	ctx := context.Background()
	project := fmt.Sprintf("brief-empty-%d", time.Now().UnixNano())

	if err := st.EnsureProject(ctx, project); err != nil {
		t.Fatalf("ensure project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project)
	})
	mine := "Q1 2026 service telemetry — reliability marts and quality audits."
	if _, err := st.UpdateProject(ctx, project, store.ProjectPatch{Summary: &mine}); err != nil {
		t.Fatalf("write the owner's description: %v", err)
	}

	if _, err := WriteProject(ctx, st, Config{Enabled: false}, project); !errors.Is(err, ErrNoAcceptedDeliveries) {
		t.Fatalf("WriteProject on a project with nothing accepted: got %v, want ErrNoAcceptedDeliveries", err)
	}
	p, err := st.GetProject(ctx, project)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.Narrative != "" {
		t.Errorf("a project with no accepted delivery carries a narrative: %q", p.Narrative)
	}
	if p.Summary != mine {
		t.Errorf("the owner's description moved: %q", p.Summary)
	}
}

// TestProjectNarrativeSeesTheLinesThatClosedWithoutAnAcceptance is Q-17's
// completeness half. The narrative was written from accepted deliveries alone,
// so a line that took most of the project's runs and closed after five attempts
// did not exist in its own project's account of what it has done. The closed
// line belongs in the state document — labelled as a line that delivered
// nothing, so the narrative can say what remains from it without claiming the
// project shipped it.
func TestProjectNarrativeSeesTheLinesThatClosedWithoutAnAcceptance(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	ctx := context.Background()
	project := fmt.Sprintf("brief-closed-%d", time.Now().UnixNano())

	acceptedRun := seedAudit(t, st, runsDir)
	closedRun := seedAudit(t, st, runsDir)
	for _, id := range []string{acceptedRun, closedRun} {
		if _, err := st.Pool.Exec(ctx, `UPDATE runs SET project=$2 WHERE run_id=$1 OR parent_run=$1`, id, project); err != nil {
			t.Fatalf("scope %s to the project: %v", id, err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET state='passed' WHERE run_id=$1`, acceptedRun); err != nil {
		t.Fatalf("pass the accepted run: %v", err)
	}
	now := time.Now().UTC()
	decide := func(runID, verdict string) {
		t.Helper()
		if _, err := st.CreateDecision(ctx, store.Decision{
			ID: store.OutcomeDecisionID(runID), Title: "Outcome", Decision: verdict,
			Tags: []string{store.OutcomeTag}, Project: project, DecidedAt: &now,
		}); err != nil {
			t.Fatalf("decide %s: %v", runID, err)
		}
	}
	decide(acceptedRun, "accepted")
	decide(closedRun, "changes_requested")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project)
	})

	for _, runID := range []string{acceptedRun, closedRun} {
		doc, err := Collect(ctx, st, runID, store.BriefSubjectDelivery)
		if err != nil {
			t.Fatalf("collect %s: %v", runID, err)
		}
		doc.Project = project
		row, err := Write(ctx, st, Config{Enabled: false}, doc)
		if err != nil {
			t.Fatalf("write delivery brief for %s: %v", runID, err)
		}
		t.Cleanup(func() { deleteBrief(t, st, row.ID) })
	}

	doc, err := CollectProject(ctx, st, project)
	if err != nil {
		t.Fatalf("collect project: %v", err)
	}
	if len(doc.Sources) != 1 || doc.Sources[0].SubjectID != acceptedRun {
		t.Fatalf("the accepted delivery is the only SOURCE; got %d sources", len(doc.Sources))
	}
	if len(doc.ClosedLines) != 1 {
		t.Fatalf("the closed line is missing from the state document: %+v", doc.ClosedLines)
	}
	closed := doc.ClosedLines[0]
	if closed.RunID != closedRun {
		t.Errorf("closed line names run %q, want %q", closed.RunID, closedRun)
	}
	if closed.Verdict != "changes requested" {
		t.Errorf("closed line's verdict = %q, want it spelled in plain words", closed.Verdict)
	}
	if closed.Attempts < 1 || closed.State == "" {
		t.Errorf("a closed line must carry its attempt count and final state: %+v", closed)
	}

	text := doc.Text()
	if !strings.Contains(text, "DELIVERY LINES THAT CLOSED WITHOUT AN ACCEPTANCE") {
		t.Error("the state document has no section for the lines that closed without an acceptance")
	}
	if !strings.Contains(text, closedRun) {
		t.Errorf("the closed line's run is not in the rendered document:\n%s", text)
	}
	if strings.Contains(text, "changes_requested") {
		t.Error("the raw enum token is still what the generator would be shown")
	}
}

func deleteBrief(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if id == "" {
		return
	}
	if _, err := st.Pool.Exec(context.Background(), `DELETE FROM briefs WHERE id=$1`, id); err != nil {
		t.Logf("cleanup brief %s: %v", id, err)
	}
}

// TestWriteKeepsAFlaggedDraftWhenTheRetryFails: the retry exists to remove an
// invented number, not to throw away a brief. When the second call fails
// outright, the flagged first draft is what the reader gets — it says more than
// the deterministic one, and its numbers are marked unverified either way.
func TestWriteKeepsAFlaggedDraftWhenTheRetryFails(t *testing.T) {
	st := briefStore(t)
	runsDir := t.TempDir()
	runID := seedAudit(t, st, runsDir)
	ctx := context.Background()

	doc, err := Collect(ctx, st, runID, store.BriefSubjectRun)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var prompts []string
	row, err := Write(ctx, st, Config{Enabled: true, RunsDir: runsDir, Codex: fakeEngine(&prompts,
		Doc{WhatHappened: "The review failed after checking 9,412 rows."},
		errors.New("codex timed out"),
	)}, doc)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { deleteBrief(t, st, row.ID) })

	if row.Engine != store.BriefEngineCodex || row.Grounded {
		t.Errorf("expected the flagged first draft, got engine=%q grounded=%v", row.Engine, row.Grounded)
	}
	if !strings.Contains(ParseDoc(row.Body).WhatHappened, "9,412") {
		t.Errorf("the first draft was discarded: %q", row.Body)
	}
}

func TestCollectDoesNotBorrowSiblingOrLooseArtifactEvidence(t *testing.T) {
	st := briefStore(t)
	ctx := context.Background()
	root := seedAudit(t, st, "")
	source, err := st.GetRun(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	child := source
	child.RunID = root + "-continuation"
	child.ParentRun = root
	child.State = "passed"
	child.Options = store.JSON(fmt.Sprintf(`{"runtime":"codex-sdk","continuation":{"sourceRunId":%q,"rootRunId":%q},"request":{"workflow":"node-workflow","nodes":[{"id":"write","kind":"task"}]}}`, root, root))
	child.Doc = store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"write","phase":"passed","verification":"not_requested"}]}}`)
	if err := st.CreateRun(ctx, child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), child.RunID) })
	sibling := child
	sibling.RunID = root + "-sibling"
	sibling.Doc = store.JSON(`{"sdk":{"phase":"passed","findings":["SIBLING PRIVATE CONCLUSION 98765"],"nodes":[{"id":"write","phase":"passed"}]}}`)
	if err := st.CreateRun(ctx, sibling); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), sibling.RunID) })
	dir := t.TempDir()
	for _, id := range []string{root, child.RunID, sibling.RunID} {
		artifacts := filepath.Join(dir, id, "artifacts")
		if err := os.MkdirAll(artifacts, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifacts, "scorecard.json"), []byte(`{"unverified":98765}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := Collect(ctx, st, child.RunID, store.BriefSubjectRun)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContinuedFrom != root {
		t.Fatalf("continuation provenance = %q", doc.ContinuedFrom)
	}
	if total, passed := doc.ProvenCount(); total != 0 || passed != 0 {
		t.Fatalf("ordinary task inherited checks: %+v", doc.Criteria)
	}
	text := doc.Text()
	for _, forbidden := range []string{"98765", "SIBLING PRIVATE", "two approved rules", "scorecard-review"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("foreign evidence %q reached brief: %s", forbidden, text)
		}
	}
	if !strings.Contains(Fallback(doc).WhatHappened, "Completion alone does not establish independently checked correctness") {
		t.Fatal("ordinary completion falsely implied correctness")
	}
	// The source remains about its own failed review after a successor succeeds.
	original, err := Collect(ctx, st, root, store.BriefSubjectRun)
	if err != nil {
		t.Fatal(err)
	}
	if original.State != "failed" || original.FailingNode != "scorecard-review" {
		t.Fatalf("successor rewrote source: %+v", original)
	}
}
