package httpapi_test

// Endpoint-level tests for the outcome briefs: what the two GETs serve, and the
// hook that writes a delivery brief the moment a human records a verdict.
//
// No test here reaches a real engine. The server is built with a hand-made
// config, whose Briefs field is false, so every brief written in this file is
// the platform's own deterministic one — which is exactly what the endpoints
// have to serve honestly.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// briefBody mirrors the wire shape of GET …/brief.
type briefBody struct {
	ID                string   `json:"id"`
	SubjectKind       string   `json:"subjectKind"`
	SubjectID         string   `json:"subjectId"`
	WhatItBuilds      string   `json:"whatItBuilds"`
	WhatHappened      string   `json:"whatHappened"`
	TrueOutcome       string   `json:"trueOutcome"`
	NextSteps         string   `json:"nextSteps"`
	Grounded          bool     `json:"grounded"`
	UngroundedNumbers []string `json:"ungroundedNumbers"`
	Engine            string   `json:"engine"`
}

// insertBrief writes a brief row directly, so a read test does not depend on
// how one gets generated.
func insertBrief(t *testing.T, st *store.Store, kind, subject, body string, grounded bool, ungrounded, engine string) store.Brief {
	t.Helper()
	row, err := st.InsertBrief(context.Background(), store.Brief{
		ID:          fmt.Sprintf("brief-%s-%d", kind, time.Now().UnixNano()),
		SubjectKind: kind, SubjectID: subject, Project: "hyperplane",
		Body: body, Grounded: grounded, UngroundedNumbers: ungrounded,
		Engine: engine, InputsDigest: "test",
	})
	if err != nil {
		t.Fatalf("insert brief: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM briefs WHERE id=$1`, row.ID)
	})
	return row
}

// TestRunBriefServesTheFourPartsAndTheFlags: the endpoint has to hand the UI
// everything it needs to render the brief AND to badge it. A grounded flag the
// client cannot see is a flag that does not exist.
func TestRunBriefServesTheFourPartsAndTheFlags(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "failed")

	body := `{"whatItBuilds":"A weekly report.","whatHappened":"The run stopped at the review gate.",` +
		`"trueOutcome":"You have a draft nobody has checked.","nextSteps":"Read the gate verdict."}`
	insertBrief(t, st, store.BriefSubjectRun, runID, body, false, "9,412", "codex")

	var got briefBody
	getJSONInto(t, ts.URL+"/api/runs/"+runID+"/brief", &got)

	if got.WhatItBuilds == "" || got.WhatHappened == "" || got.TrueOutcome == "" || got.NextSteps == "" {
		t.Fatalf("the four parts must all be on the wire: %+v", got)
	}
	if got.Grounded {
		t.Error("a brief stored as ungrounded must not be served as grounded")
	}
	if len(got.UngroundedNumbers) != 1 || got.UngroundedNumbers[0] != "9,412" {
		t.Errorf("the unverified numbers must be named so the badge can name them: %+v", got.UngroundedNumbers)
	}
	if got.Engine != "codex" {
		t.Errorf("engine = %q, want codex", got.Engine)
	}
}

// TestRunBriefServesTheLatest: insert-only with history kept means the read has
// to be unambiguous about which one is current.
func TestRunBriefServesTheLatest(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "failed")

	insertBrief(t, st, store.BriefSubjectRun, runID,
		`{"whatHappened":"first"}`, true, "", "fallback")
	time.Sleep(5 * time.Millisecond)
	newest := insertBrief(t, st, store.BriefSubjectRun, runID,
		`{"whatHappened":"second, after the repair"}`, true, "", "codex")

	var got briefBody
	getJSONInto(t, ts.URL+"/api/runs/"+runID+"/brief", &got)
	if got.ID != newest.ID || !strings.Contains(got.WhatHappened, "after the repair") {
		t.Errorf("the latest brief must win for display, got %+v", got)
	}
}

// TestRunBriefIsHonestlyAbsent: a run nobody has briefed yet answers 404, not
// an empty brief. An empty account reads as "nothing happened".
func TestRunBriefIsHonestlyAbsent(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")

	resp, err := http.Get(ts.URL + "/api/runs/" + runID + "/brief")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("un-briefed run: expected 404, got %d", resp.StatusCode)
	}

	// And a path that could escape the runs directory is refused outright.
	bad, err := http.Get(ts.URL + "/api/runs/" + "..%2F..%2Fetc" + "/brief")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest && bad.StatusCode != http.StatusNotFound {
		t.Errorf("a traversal-shaped run id must be refused, got %d", bad.StatusCode)
	}
}

// TestDeliveryBriefAcceptsEitherId: the decision surfaces hold the decision id,
// the run surfaces hold the run id, and both are asking for the same brief.
func TestDeliveryBriefAcceptsEitherId(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")
	insertBrief(t, st, store.BriefSubjectDelivery, runID,
		`{"whatHappened":"Accepted, with a note about the missing rules."}`, true, "", "fallback")

	for _, id := range []string{runID, store.OutcomeDecisionID(runID)} {
		var got briefBody
		getJSONInto(t, ts.URL+"/api/decisions/"+id+"/brief", &got)
		if got.SubjectKind != store.BriefSubjectDelivery || !strings.Contains(got.WhatHappened, "Accepted") {
			t.Errorf("GET /api/decisions/%s/brief served the wrong thing: %+v", id, got)
		}
	}
}

// TestRecordingAnOutcomeWritesTheDeliveryBrief is the second trigger: the
// verdict is the one fact a run's own brief could never contain, and recording
// it is what produces the account that does.
//
// Asserted "eventually": the write is detached so the person recording a
// verdict never waits on prose.
func TestRecordingAnOutcomeWritesTheDeliveryBrief(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "failed")

	status, body := postOutcome(t, ts.URL+"/api/runs/"+runID+"/outcome",
		"changes_requested", "the scorecard is missing two rules")
	if status != http.StatusCreated {
		t.Fatalf("record outcome: expected 201, got %d: %s", status, body)
	}

	row := waitForBriefRow(t, st, store.BriefSubjectDelivery, runID)
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM briefs WHERE id=$1`, row.ID)
	})
	if row.Engine != store.BriefEngineFallback {
		t.Errorf("the test server has the engine switched off, so the brief must be the platform's own; got %q", row.Engine)
	}

	var got briefBody
	getJSONInto(t, ts.URL+"/api/decisions/"+store.OutcomeDecisionID(runID)+"/brief", &got)
	// The verdict, in the words a person says it in — never the database token.
	if !strings.Contains(got.WhatHappened, "changes requested") {
		t.Errorf("the delivery brief must state the verdict: %q", got.WhatHappened)
	}
	if strings.Contains(got.WhatHappened, "changes_requested") {
		t.Errorf("the raw enum token reached the reader: %q", got.WhatHappened)
	}
	if !strings.Contains(got.WhatHappened, "missing two rules") {
		t.Errorf("the person's own note must survive into the brief: %q", got.WhatHappened)
	}
	if !got.Grounded {
		t.Error("a deterministic brief is derived from the state document and is grounded by construction")
	}
}

// waitForBriefRow polls for a brief written by a detached hook.
func waitForBriefRow(t *testing.T, st *store.Store, kind, subject string) store.Brief {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		row, err := st.LatestBrief(context.Background(), kind, subject)
		if err == nil {
			return row
		}
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("latest brief: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s brief was written for %s within the deadline", kind, subject)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestBriefViewParsesAProseBody: a body that is not the four-part document is
// still served as something a person can read, rather than as an error.
func TestBriefViewParsesAProseBody(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedTerminalRun(t, st, "passed")
	insertBrief(t, st, store.BriefSubjectRun, runID, "just some prose", true, "", "fallback")

	var got briefBody
	getJSONInto(t, ts.URL+"/api/runs/"+runID+"/brief", &got)
	if got.WhatHappened != "just some prose" {
		t.Errorf("a prose body must still reach the reader, got %+v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("view does not round-trip: %v", err)
	}
}
