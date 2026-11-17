package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/learning"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// inboxBody is the queue as the web app reads it: four kinds of "your turn".
type inboxBody struct {
	Approvals  []store.Record `json:"approvals"`
	Selections []store.Record `json:"selections"`
	Outcomes   []apiJourney   `json:"outcomes"`
	Proposals  []store.Skill  `json:"proposals"`
}

func getInbox(t *testing.T, base string) inboxBody {
	t.Helper()
	resp, err := http.Get(base + "/api/inbox")
	if err != nil {
		t.Fatalf("GET /api/inbox: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/inbox: status %d", resp.StatusCode)
	}
	var body inboxBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	return body
}

func hasProposal(body inboxBody, id string) bool {
	for _, p := range body.Proposals {
		if p.ID == id {
			return true
		}
	}
	return false
}

// TestInboxSurfacesLearnedDraftsAsProposals is the inbox contract for automatic
// extraction: a draft the platform learned from a run waits in the queue as a
// proposal, and answering it — either way — takes it out.
//
// It also pins what is NOT a proposal. A draft somebody is hand-writing has no
// source run, and putting unfinished work in the queue would ask a question
// nobody asked for.
func TestInboxSurfacesLearnedDraftsAsProposals(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()

	learned := fmt.Sprintf("learned-draft-%d", time.Now().UnixNano())
	handwritten := fmt.Sprintf("hand-draft-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = st.DeleteSkill(context.Background(), learned)
		_ = st.DeleteSkill(context.Background(), handwritten)
	})

	if err := st.UpsertSkill(ctx, store.Skill{
		ID: learned, Name: "Competitive teardown", Instruction: "Fix the axes first.",
		Metadata: json.RawMessage(`{"status":"draft","sourceRun":"run-x","sourceJourney":"j-1",` +
			`"sourceDelivery":"Note-taking market scan","confidence":0.8}`),
	}); err != nil {
		t.Fatalf("seed learned draft: %v", err)
	}
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: handwritten, Name: "Still writing this", Instruction: "…",
		Metadata: json.RawMessage(`{"status":"draft"}`),
	}); err != nil {
		t.Fatalf("seed hand-written draft: %v", err)
	}

	body := getInbox(t, ts.URL)
	if !hasProposal(body, learned) {
		t.Fatalf("the learned draft is not in the inbox proposals: %+v", body.Proposals)
	}
	if hasProposal(body, handwritten) {
		t.Error("a hand-written draft with no source run must not become a proposal card")
	}

	// The card carries what it was learned from, so it can say so.
	for _, p := range body.Proposals {
		if p.ID != learned {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal(p.Metadata, &meta); err != nil {
			t.Fatalf("proposal metadata: %v", err)
		}
		if meta["sourceDelivery"] != "Note-taking market scan" || meta["sourceRun"] != "run-x" {
			t.Errorf("proposal lineage missing: %v", meta)
		}
	}

	// Approving is the human step — and it clears the card. A learned draft
	// needs its practice test first (see TestApproveLearnedSkillWithoutEvalset).
	seedSkillEvalset(t, st, learned)
	if status, body := postJSON(t, ts.URL+"/api/skills/"+learned+"/approve"); status != http.StatusOK {
		t.Fatalf("approve: %d %s", status, body)
	}
	if after := getInbox(t, ts.URL); hasProposal(after, learned) {
		t.Error("an approved proposal must leave the queue")
	}

	// So does rejecting: seed another and decline it.
	rejected := fmt.Sprintf("rejected-draft-%d", time.Now().UnixNano())
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: rejected, Name: "Not worth keeping", Instruction: "x",
		Metadata: json.RawMessage(`{"status":"draft","sourceRun":"run-y"}`),
	}); err != nil {
		t.Fatalf("seed rejectable draft: %v", err)
	}
	if !hasProposal(getInbox(t, ts.URL), rejected) {
		t.Fatalf("the second proposal never appeared")
	}
	if status, body := postJSON(t, ts.URL+"/api/skills/"+rejected+"/reject"); status != http.StatusOK {
		t.Fatalf("reject: %d %s", status, body)
	}
	if after := getInbox(t, ts.URL); hasProposal(after, rejected) {
		t.Error("a rejected proposal must leave the queue")
	}
}

// TestAcceptanceDoesNotLearnWhenAutoLearnIsOff pins the switch that keeps a
// billed engine call from firing behind anybody's back. The test server's
// Config never sets AutoLearn, so accepting a delivery must record the verdict
// and nothing else — no claim, no extraction.
func TestAcceptanceDoesNotLearnWhenAutoLearnIsOff(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")

	j := oneJourney(t, ts.URL, project)
	cleanupJourneyDecision(t, st, j.Key)
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), learning.RecordType, j.Attempts[0].RunID) })

	if status, body := postJourneyOutcome(t, ts.URL, j.Key, "accepted", "ship it"); status != http.StatusCreated {
		t.Fatalf("accept: %d %s", status, body)
	}

	// The hook is synchronous up to the point it decides not to run, so the
	// claim's absence right after the response is a real assertion.
	if _, err := st.GetRecord(context.Background(), learning.RecordType, j.Attempts[0].RunID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("automatic extraction claimed a delivery with AutoLearn off (err=%v)", err)
	}
}
