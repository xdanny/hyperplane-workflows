package httpapi_test

// Rehearsal honesty at the API boundary.
//
// A rehearsal (`mode: plan`) executes nothing: its agent nodes answer with
// stand-ins and its approval gates simulate their own verdicts. Run
// `20260820-215211.207-ml-engineering` was one, and its page read
// "PASSED · DELIVERY ACCEPTED · 4/4 PROVEN" with a human's ACCEPTED on it —
// because nothing between the mode column and the verdict ever asked.
//
// These tests pin the two refusals, at the two grains a verdict can be recorded
// at, plus the flag the surfaces read to say so.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// seedRehearsalRun creates a finished run in the given mode, with a recipe doc
// so it groups into a journey of its own.
func seedRehearsalRun(t *testing.T, st *store.Store, project, mode string) string {
	t.Helper()
	runID := fmt.Sprintf("run-rehearsal-%d-%d", time.Now().UnixNano(), journeySeq.Add(1))
	doc, err := json.Marshal(map[string]any{
		"recipe": "recipe-ml",
		"sdk":    map[string]any{"phase": "passed", "nodes": []map[string]any{{"id": "c1", "phase": "passed", "verification": "passed"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": map[string]any{"workflow": "node-workflow", "nodes": []map[string]any{{"id": "c1", "title": "The metric clears the declared threshold", "kind": "task", "check": []string{"sh", "checks.sh"}}}}})
	now := time.Now().UTC()
	run := store.Run{
		RunID: runID, DAG: "ml-engineering", Goal: "baseline the tip model",
		State: "passed", Mode: mode, Project: project, Doc: doc, Options: options,
		StartedAt: now, EndedAt: now.Add(time.Second),
	}
	if err := st.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = st.DeleteDecision(ctx, store.OutcomeDecisionID(runID))
		_ = st.DeleteRun(ctx, runID)
	})
	time.Sleep(time.Millisecond)
	return runID
}

// TestRunOutcomeRefusesAcceptingARehearsal: run grain. The other two verdicts
// stay open — "this plan is not good enough" is an honest thing to record.
func TestRunOutcomeRefusesAcceptingARehearsal(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)

	for _, verdict := range []string{"accepted", "changes_requested", "rejected"} {
		t.Run(verdict, func(t *testing.T) {
			runID := seedRehearsalRun(t, st, newProject(t), "plan")
			url := ts.URL + "/api/runs/" + runID + "/outcome"
			status, body := postOutcome(t, url, verdict, "")

			if verdict != "accepted" {
				if status != http.StatusCreated {
					t.Fatalf("%s on a rehearsal must still be recordable, got %d: %s", verdict, status, body)
				}
				return
			}
			if status != http.StatusConflict {
				t.Fatalf("accepting a rehearsal must be refused with 409, got %d: %s", status, body)
			}
			var refusal struct {
				Error     string `json:"error"`
				Rehearsal bool   `json:"rehearsal"`
			}
			if err := json.Unmarshal(body, &refusal); err != nil {
				t.Fatalf("decode refusal: %v", err)
			}
			if !refusal.Rehearsal {
				t.Error("the refusal must say WHICH rule refused it")
			}
			if refusal.Error != store.RehearsalSentence {
				t.Errorf("refusal reads %q, want the one plain sentence %q", refusal.Error, store.RehearsalSentence)
			}
			if _, err := st.GetRunOutcome(context.Background(), runID); err == nil {
				t.Error("a refused acceptance must write no decision")
			}
		})
	}
}

// TestRunOutcomeStillAcceptsARealRun is the control: the refusal must key on the
// mode column and nothing else.
func TestRunOutcomeStillAcceptsARealRun(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	runID := seedRehearsalRun(t, st, newProject(t), "run")

	status, body := postOutcome(t, ts.URL+"/api/runs/"+runID+"/outcome", "accepted", "shipped")
	if status != http.StatusCreated {
		t.Fatalf("a real run must still be acceptable, got %d: %s", status, body)
	}
}

// TestJourneyOutcomeRefusesAcceptingARehearsal: delivery grain — the one the
// inbox actually asks at, and the one whose acceptance fires skill extraction.
func TestJourneyOutcomeRefusesAcceptingARehearsal(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	seedRehearsalRun(t, st, project, "plan")

	j := oneJourney(t, ts.URL, project)
	if !j.Rehearsal {
		t.Fatal("a delivery whose every attempt was a dry run must be flagged as a rehearsal")
	}
	if len(j.Attempts) != 1 || !j.Attempts[0].Rehearsal {
		t.Errorf("the attempt itself must carry the flag: %+v", j.Attempts)
	}
	cleanupJourneyDecision(t, st, j.Key)

	status, body := postJourneyOutcome(t, ts.URL, j.Key, "accepted", "", j.Attempts[0].RunID)
	if status != http.StatusConflict {
		t.Fatalf("accepting a rehearsed delivery must be refused with 409, got %d: %s", status, body)
	}
	if !strings.Contains(string(body), "cannot be accepted as a delivery") {
		t.Errorf("the refusal must say why in plain words: %s", body)
	}

	// Rejecting is still allowed — a plan can be judged not worth running.
	if status, body := postJourneyOutcome(t, ts.URL, j.Key, "rejected", "", j.Attempts[0].RunID); status != http.StatusCreated {
		t.Fatalf("rejecting a rehearsal must stay allowed, got %d: %s", status, body)
	}
}

// Acceptance applies to the viewed latest attempt, even if an earlier attempt ran for real.
func TestJourneyOutcomeRefusesNewestRehearsalAfterRealAttempt(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	seedJourneyRun(t, st, project, "ml-engineering", "recipe-ml", "passed", "c1")
	seedRehearsalRun(t, st, project, "plan")

	j := oneJourney(t, ts.URL, project)
	if j.AttemptCount != 2 {
		t.Fatalf("attempts = %d, want 2", j.AttemptCount)
	}
	if j.Rehearsal {
		t.Error("a delivery with one REAL attempt is not a rehearsal")
	}
	cleanupJourneyDecision(t, st, j.Key)
	if status, body := postJourneyOutcome(t, ts.URL, j.Key, "accepted", "", j.Attempts[0].RunID); status != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", status, body)
	}
}

// TestLearnedDraftsFromARehearsalAreFlagged: the existing bad draft is not
// deleted — a draft is a record of what the platform once proposed — but every
// surface that renders it must stop saying "Learned from a passed run you
// accepted". The flag is computed at read time from the run's mode, so a draft
// written long before the rule reads honestly without a backfill.
func TestLearnedDraftsFromARehearsalAreFlagged(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()

	rehearsed := seedRehearsalRun(t, st, newProject(t), "plan")
	real := seedRehearsalRun(t, st, newProject(t), "run")

	type draft struct {
		id      string
		run     string
		wantFlg bool
	}
	drafts := []draft{
		{fmt.Sprintf("draft-rehearsed-%d", time.Now().UnixNano()), rehearsed, true},
		{fmt.Sprintf("draft-real-%d", time.Now().UnixNano()), real, false},
	}
	for _, d := range drafts {
		meta, _ := json.Marshal(map[string]any{
			"status": store.SkillStatusDraft, "sourceRun": d.run, "sourceRuns": []string{d.run},
		})
		if err := st.InsertSkill(ctx, store.Skill{
			ID: d.id, Name: d.id, Description: "learned method",
			Instruction: "do the thing", Metadata: meta,
		}); err != nil {
			t.Fatalf("insert draft %s: %v", d.id, err)
		}
		t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), d.id) })
	}

	// Both surfaces that carry an Approve button read the flag.
	for _, surface := range []struct {
		name string
		read func() map[string]bool
	}{
		{"registry", func() map[string]bool {
			var skills []struct {
				ID       string         `json:"id"`
				Metadata map[string]any `json:"metadata"`
			}
			getJSONInto(t, ts.URL+"/api/skills?limit=500", &skills)
			out := map[string]bool{}
			for _, s := range skills {
				out[s.ID] = s.Metadata["sourceRunRehearsal"] == true
			}
			return out
		}},
		{"inbox", func() map[string]bool {
			var body struct {
				Proposals []struct {
					ID       string         `json:"id"`
					Metadata map[string]any `json:"metadata"`
				} `json:"proposals"`
			}
			getJSONInto(t, ts.URL+"/api/inbox", &body)
			out := map[string]bool{}
			for _, s := range body.Proposals {
				out[s.ID] = s.Metadata["sourceRunRehearsal"] == true
			}
			return out
		}},
	} {
		t.Run(surface.name, func(t *testing.T) {
			got := surface.read()
			for _, d := range drafts {
				flagged, present := got[d.id]
				if !present {
					t.Fatalf("draft %s is missing from the %s", d.id, surface.name)
				}
				if flagged != d.wantFlg {
					t.Errorf("draft %s (source run mode) flagged = %v, want %v", d.id, flagged, d.wantFlg)
				}
			}
		})
	}

	// And the button the flag disables is refused by the server too.
	status, body := postJSON(t, ts.URL+"/api/skills/"+drafts[0].id+"/approve")
	if status != http.StatusConflict {
		t.Fatalf("approving a rehearsal-sourced draft must 409, got %d: %s", status, body)
	}
	if !strings.Contains(string(body), "stand-ins") {
		t.Errorf("the refusal must say why: %s", body)
	}
}
