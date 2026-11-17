package httpapi_test

// The BACKFILL endpoints: POST /api/runs/{id}/brief and
// POST /api/projects/{id}/narrative.
//
// Both triggers only fire forward, so work that finished before briefs existed
// has no account of itself and re-running it to get one would be absurd. These
// two are how it gets one.
//
// Nothing here reaches a real engine: the server's brief engine is replaced
// with a canned answer through the test-only seam (export_test.go), which is
// the same shape internal/brief offers its own callers.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/brief"
	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// fakeBriefEngine answers every generation call with one canned brief and
// counts the calls. No codex, no subprocess, no bill.
func fakeBriefEngine(calls *int, d brief.Doc) brief.CodexFunc {
	return func(_ context.Context, o codex.Options) (codex.Result, error) {
		*calls++
		if o.SandboxMode != codex.SandboxReadOnly {
			return codex.Result{}, fmt.Errorf("a brief must run read-only, got %q", o.SandboxMode)
		}
		b, err := json.Marshal(d)
		if err != nil {
			return codex.Result{}, err
		}
		return codex.Result{OK: true, JSON: b}, nil
	}
}

// briefsOn builds a test server with HYPERPLANE_BRIEFS on and the engine faked,
// which is the shape the backfill endpoints are actually used in.
func briefsOn(t *testing.T, calls *int, d brief.Doc) *httptest.Server {
	t.Helper()
	srv, ts := newTestServer(t, func(c *config.Config) { c.Briefs = true })
	srv.SetBriefEngine(fakeBriefEngine(calls, d))
	return ts
}

func postTo(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, buf.Bytes()
}

// dropBriefs removes every brief written about a subject, so a test leaves the
// table as it found it.
func dropBriefs(t *testing.T, st *store.Store, subject string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM briefs WHERE subject_id=$1`, subject)
	})
}

// TestWriteRunBriefBackfillsAFinishedRun is why this endpoint exists: a run
// that finished before briefs existed has no account of itself, and the only
// other way to get one would be to run the work again.
func TestWriteRunBriefBackfillsAFinishedRun(t *testing.T) {
	calls := 0
	ts := briefsOn(t, &calls, brief.Doc{
		WhatItBuilds: "A weekly report on the taxi trips table.",
		WhatHappened: "The run reached the end of its graph. No check ran, so nothing was verified.",
		TrueOutcome:  "You have a report nobody checked.",
		NextSteps:    "Add a verify step before trusting it.",
	})
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")
	dropBriefs(t, st, runID)

	// Nothing yet — the trigger never fired for this run.
	resp, err := http.Get(ts.URL + "/api/runs/" + runID + "/brief")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected no brief before the backfill, got %d", resp.StatusCode)
	}

	status, body := postTo(t, ts.URL+"/api/runs/"+runID+"/brief", "{}")
	if status != http.StatusCreated {
		t.Fatalf("backfill: expected 201, got %d: %s", status, body)
	}
	var written briefBody
	if err := json.Unmarshal(body, &written); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if calls != 1 {
		t.Errorf("expected exactly one engine call, got %d", calls)
	}
	if written.Engine != "codex" || !written.Grounded {
		t.Errorf("expected a grounded engine brief, got engine=%q grounded=%v", written.Engine, written.Grounded)
	}
	if !strings.Contains(written.TrueOutcome, "nobody checked") {
		t.Errorf("the engine's brief did not survive to the wire: %+v", written)
	}

	// And it is now the run's brief on the read path the UI uses.
	var got briefBody
	getJSONInto(t, ts.URL+"/api/runs/"+runID+"/brief", &got)
	if got.ID != written.ID {
		t.Errorf("GET serves %q, POST wrote %q", got.ID, written.ID)
	}

	// Idempotent in the way this table is: a second call is a second row and the
	// newest wins. Nothing is overwritten, so the earlier account survives — a
	// brief written before a repair and one written after are exactly the pair a
	// reader wants both halves of.
	status, body = postTo(t, ts.URL+"/api/runs/"+runID+"/brief", "")
	if status != http.StatusCreated {
		t.Fatalf("second backfill: expected 201, got %d: %s", status, body)
	}
	var second briefBody
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if second.ID == written.ID {
		t.Error("a re-run must insert a new row, not overwrite the first")
	}
	getJSONInto(t, ts.URL+"/api/runs/"+runID+"/brief", &got)
	if got.ID != second.ID {
		t.Errorf("the latest brief must win for display, got %q", got.ID)
	}
}

// TestWriteRunBriefHonoursTheSwitch: HYPERPLANE_BRIEFS=0 does not switch briefs
// off, it switches the BILLED CALL off. The backfill still produces an account.
func TestWriteRunBriefHonoursTheSwitch(t *testing.T) {
	calls := 0
	srv, ts := newTestServer(t, func(c *config.Config) { c.Briefs = false })
	srv.SetBriefEngine(fakeBriefEngine(&calls, brief.Doc{WhatHappened: "should never be asked for"}))
	st := openStore(t)
	runID := seedTerminalRun(t, st, "failed")
	dropBriefs(t, st, runID)

	status, body := postTo(t, ts.URL+"/api/runs/"+runID+"/brief", "{}")
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", status, body)
	}
	var written briefBody
	if err := json.Unmarshal(body, &written); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if calls != 0 {
		t.Errorf("with the switch off nothing may reach the engine; it made %d call(s)", calls)
	}
	if written.Engine != store.BriefEngineFallback {
		t.Errorf("engine = %q, want the deterministic fallback", written.Engine)
	}
	if !strings.Contains(written.WhatHappened, "failed") {
		t.Errorf("the fallback must still state what happened: %q", written.WhatHappened)
	}
}

// TestWriteRunBriefRefusesWhatItCannotAccountFor: a run still going has no
// outcome, and a delivery with no verdict has no delivery to brief. Both are
// refused with a sentence saying what to do instead — writing either anyway
// would be this feature inventing the thing it exists to report.
func TestWriteRunBriefRefusesWhatItCannotAccountFor(t *testing.T) {
	calls := 0
	ts := briefsOn(t, &calls, brief.Doc{WhatHappened: "never"})
	st := openStore(t)

	running := seedTerminalRun(t, st, "running")
	status, body := postTo(t, ts.URL+"/api/runs/"+running+"/brief", "{}")
	if status != http.StatusConflict {
		t.Errorf("a running run: expected 409, got %d: %s", status, body)
	}

	done := seedTerminalRun(t, st, "passed")
	status, body = postTo(t, ts.URL+"/api/runs/"+done+"/brief", `{"subject":"delivery"}`)
	if status != http.StatusConflict {
		t.Errorf("an undecided delivery: expected 409, got %d: %s", status, body)
	}
	if !strings.Contains(string(body), "no delivery decision") {
		t.Errorf("the refusal must say what is missing: %s", body)
	}

	if status, _ = postTo(t, ts.URL+"/api/runs/"+done+"/brief", `{"subject":"nonsense"}`); status != http.StatusBadRequest {
		t.Errorf("an unknown subject: expected 400, got %d", status)
	}
	if status, _ = postTo(t, ts.URL+"/api/runs/nope-not-a-run/brief", "{}"); status != http.StatusNotFound {
		t.Errorf("an unknown run: expected 404, got %d", status)
	}
	if status, _ = postTo(t, ts.URL+"/api/runs/"+"..%2F..%2Fetc"+"/brief", "{}"); status != http.StatusBadRequest && status != http.StatusNotFound {
		t.Errorf("a traversal-shaped run id must be refused, got %d", status)
	}

	if calls != 0 {
		t.Errorf("nothing that was refused may have reached the engine; it made %d call(s)", calls)
	}
}

// TestWriteRunBriefBackfillsADelivery: once a verdict exists, the delivery
// brief can be backfilled too — the account that includes what a person decided.
func TestWriteRunBriefBackfillsADelivery(t *testing.T) {
	calls := 0
	ts := briefsOn(t, &calls, brief.Doc{
		WhatItBuilds: "A weekly report.",
		WhatHappened: "A person accepted this delivery.",
		TrueOutcome:  "An accepted report.",
		NextSteps:    "Nothing — this is finished.",
	})
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")
	dropBriefs(t, st, runID)

	now := time.Now().UTC()
	if _, err := st.CreateDecision(context.Background(), store.Decision{
		ID: store.OutcomeDecisionID(runID), Title: "Outcome", Decision: "accepted",
		Tags: []string{store.OutcomeTag}, Project: "hyperplane", DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}

	status, body := postTo(t, ts.URL+"/api/runs/"+runID+"/brief", `{"subject":"delivery"}`)
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", status, body)
	}
	var got briefBody
	getJSONInto(t, ts.URL+"/api/decisions/"+store.OutcomeDecisionID(runID)+"/brief", &got)
	if got.SubjectKind != store.BriefSubjectDelivery || !strings.Contains(got.WhatHappened, "accepted") {
		t.Errorf("the delivery brief is not on the delivery read path: %+v", got)
	}
}

// TestWriteProjectNarrativeBackfills is the other half: a project whose
// deliveries were accepted before briefs existed gets its "what this project
// has built so far" without anybody re-deciding anything.
//
// It also pins the refusal, which matters more than it looks: with nothing
// accepted, NOTHING is written. `projects.summary` may hold a description a
// person typed, and replacing it with a statement of absence would destroy real
// information to say something the empty narrative already implies.
func TestWriteProjectNarrativeBackfills(t *testing.T) {
	calls := 0
	ts := briefsOn(t, &calls, brief.Doc{
		WhatItBuilds: "This project has delivered one weekly report on the taxi trips table.",
		WhatHappened: "One delivery has been accepted.",
		TrueOutcome:  "An accepted report, checked by nothing.",
		NextSteps:    "Add a verify step.",
	})
	st := openStore(t)
	ctx := context.Background()
	project := fmt.Sprintf("brief-api-proj-%d", time.Now().UnixNano())
	dropBriefs(t, st, project)
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project)
	})

	status, body := postTo(t, ts.URL+"/api/projects/"+project+"/narrative", "")
	if status != http.StatusConflict {
		t.Fatalf("an empty project: expected 409, got %d: %s", status, body)
	}
	if calls != 0 {
		t.Errorf("a refused narrative must not reach the engine; it made %d call(s)", calls)
	}

	// An accepted delivery with a brief of its own is what a narrative is
	// written from.
	runID := seedTerminalRun(t, st, "passed")
	dropBriefs(t, st, runID)
	if _, err := st.Pool.Exec(ctx, `UPDATE runs SET project=$2 WHERE run_id=$1`, runID, project); err != nil {
		t.Fatalf("scope the run: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateDecision(ctx, store.Decision{
		ID: store.OutcomeDecisionID(runID), Title: "Outcome", Decision: "accepted",
		Tags: []string{store.OutcomeTag}, Project: project, DecidedAt: &now,
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	insertBriefIn(t, st, store.BriefSubjectDelivery, runID, project,
		`{"whatItBuilds":"A weekly report.","trueOutcome":"An accepted report."}`)

	status, body = postTo(t, ts.URL+"/api/projects/"+project+"/narrative", "")
	if status != http.StatusCreated {
		t.Fatalf("narrative: expected 201, got %d: %s", status, body)
	}
	var resp struct {
		Brief      briefBody `json:"brief"`
		Narrative  string    `json:"narrative"`
		Deliveries int       `json:"deliveries"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Deliveries != 1 {
		t.Errorf("narrative written from %d deliveries, want 1", resp.Deliveries)
	}
	if !strings.Contains(resp.Narrative, "one weekly report") {
		t.Errorf("the narrative did not land on projects.narrative: %q", resp.Narrative)
	}

	// And it really is on the project row the card reads — under the machine's
	// heading, never under the owner's.
	p, err := st.GetProject(ctx, project)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.Narrative != resp.Narrative {
		t.Errorf("projects.narrative is %q, the response said %q", p.Narrative, resp.Narrative)
	}
	if p.Summary != "" {
		t.Errorf("regenerating the narrative wrote into the owner's description: %q", p.Summary)
	}
}

// TestJourneyAcceptanceBriefsTheDeliveryAndTheProject covers the grain the
// inbox actually uses.
//
// A journey-grain verdict is recorded against the DELIVERY, not against one
// run, so `outcome-<runId>` finds nothing for it. Everything downstream — the
// delivery brief's verdict line, and whether a delivery counts towards the
// project narrative — has to look at both grains, or it reports "nobody has
// decided" about deliveries people decided through the queue.
func TestJourneyAcceptanceBriefsTheDeliveryAndTheProject(t *testing.T) {
	calls := 0
	ts := briefsOn(t, &calls, brief.Doc{
		WhatItBuilds: "A weekly report.",
		WhatHappened: "A person accepted this delivery through the queue.",
		TrueOutcome:  "An accepted report.",
		NextSteps:    "Nothing — this is finished.",
	})
	st := openStore(t)
	project := fmt.Sprintf("brief-journey-proj-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, project)
	})

	runID := seedJourneyRun(t, st, project, "ship-report", "recipe-report", "passed", "c1")
	dropBriefs(t, st, runID)
	dropBriefs(t, st, project)

	j := oneJourney(t, ts.URL, project)
	cleanupJourneyDecision(t, st, j.Key)
	if status, body := postJourneyOutcome(t, ts.URL, j.Key, "accepted", "good enough to ship"); status != http.StatusCreated {
		t.Fatalf("record journey outcome: expected 201, got %d: %s", status, body)
	}

	// The hook is detached, so this is an "eventually".
	row := waitForBriefRow(t, st, store.BriefSubjectDelivery, runID)
	if !strings.Contains(brief.ParseDoc(row.Body).Text(), "accepted") {
		t.Errorf("the delivery brief does not carry the verdict: %s", row.Body)
	}

	// And the acceptance counted towards the project narrative — the run-grain
	// lookup alone would have found no accepted delivery here at all.
	deadline := time.Now().Add(15 * time.Second)
	for {
		p, err := st.GetProject(context.Background(), project)
		if err == nil && strings.TrimSpace(p.Narrative) != "" {
			if !strings.Contains(p.Narrative, "weekly report") {
				t.Errorf("projects.narrative is not the narrative: %q", p.Narrative)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the project narrative was never written after a journey-grain acceptance")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// insertBriefIn writes a brief row scoped to a project.
func insertBriefIn(t *testing.T, st *store.Store, kind, subject, project, body string) {
	t.Helper()
	row, err := st.InsertBrief(context.Background(), store.Brief{
		ID:          fmt.Sprintf("brief-%s-%d", kind, time.Now().UnixNano()),
		SubjectKind: kind, SubjectID: subject, Project: project,
		Body: body, Grounded: true, Engine: store.BriefEngineFallback, InputsDigest: "test",
	})
	if err != nil {
		t.Fatalf("insert brief: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM briefs WHERE id=$1`, row.ID)
	})
}
