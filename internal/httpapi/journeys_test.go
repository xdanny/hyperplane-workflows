package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// journeySeq keeps seeded run ids unique within a package run.
var journeySeq atomic.Int64

// apiJourney mirrors the journey wire shape the web app reads.
type apiJourney struct {
	Key           string `json:"key"`
	Project       string `json:"project"`
	Name          string `json:"name"`
	DAG           string `json:"dag"`
	Recipe        string `json:"recipe"`
	Root          string `json:"root"`
	Stage         string `json:"stage"`
	Maturity      string `json:"maturity"`
	AttemptCount  int    `json:"attemptCount"`
	Criteria      int    `json:"criteria"`
	Proven        int    `json:"proven"`
	NeedsDecision bool   `json:"needsDecision"`
	Rehearsal     bool   `json:"rehearsal"`
	Attempts      []struct {
		RunID         string `json:"runId"`
		Number        int    `json:"number"`
		State         string `json:"state"`
		Superseded    bool   `json:"superseded"`
		Rehearsal     bool   `json:"rehearsal"`
		Criteria      int    `json:"criteria"`
		Proven        int    `json:"proven"`
		ContinuedFrom string `json:"continuedFrom"`
	} `json:"attempts"`
	Outcome *struct {
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
		Scope   string `json:"scope"`
		RunID   string `json:"runId"`
	} `json:"outcome"`
	Timeline []struct {
		Kind  string `json:"kind"`
		Text  string `json:"text"`
		RunID string `json:"runId"`
	} `json:"timeline"`
}

// newProject returns a project tag no other test shares, so a journey list can
// be asserted exactly.
func newProject(t *testing.T) string {
	t.Helper()
	p := fmt.Sprintf("journey-test-%d-%d", time.Now().UnixNano(), journeySeq.Add(1))
	t.Cleanup(func() { _, _ = openStore(t).Pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, p) })
	return p
}

// seedJourneyRun creates an SDK run with its frozen request and checkpoint.
// Ordinary task completion supplies no independently checked criteria.
func seedJourneyRun(t *testing.T, st *store.Store, project, dag, recipe, state string, criteria ...string) string {
	t.Helper()
	runID := fmt.Sprintf("run-journey-%d-%d", time.Now().UnixNano(), journeySeq.Add(1))
	doc := map[string]any{}
	if recipe != "" {
		doc["recipe"] = recipe
	}
	nodes := []map[string]any{{"id": "draft", "title": "Write files", "kind": "task", "instruction": "Write useful files"}}
	progress := []map[string]any{{"id": "draft", "phase": state, "verification": "not_requested"}}
	for _, id := range criteria {
		nodes = append(nodes, map[string]any{"id": id, "title": id, "kind": "check", "instruction": "Review the declared result"})
		progress = append(progress, map[string]any{"id": id, "phase": state})
	}
	doc["sdk"] = map[string]any{"phase": state, "nodes": progress}
	options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": map[string]any{"workflow": "node-workflow", "nodes": nodes}})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := store.Run{
		RunID: runID, DAG: dag, Goal: "ship " + dag, State: state, Project: project,
		Doc: raw, Options: options, StartedAt: now,
	}
	if state == "passed" || state == "failed" || state == "interrupted" {
		run.EndedAt = now.Add(time.Second)
	}
	if err := st.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = st.DeleteDecision(ctx, store.OutcomeDecisionID(runID))
		_ = st.DeleteRun(ctx, runID)
	})
	// Runs seeded back-to-back can land on the same started_at; the derivation
	// tie-breaks on run id, and these ids are monotonic.
	time.Sleep(time.Millisecond)
	return runID
}

// listJourneys fetches the journeys for a project.
func listJourneys(t *testing.T, base, project string) []apiJourney {
	t.Helper()
	var out []apiJourney
	getJSONInto(t, base+"/api/journeys?project="+project, &out)
	return out
}

// oneJourney fetches the single journey expected in a project.
func oneJourney(t *testing.T, base, project string) apiJourney {
	t.Helper()
	list := listJourneys(t, base, project)
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 journey in %s, got %d", project, len(list))
	}
	return list[0]
}

// postJourneyOutcome records a journey-grain verdict.
func postJourneyOutcome(t *testing.T, base, key, verdict, note string, viewedRunID ...string) (int, []byte) {
	t.Helper()
	runID := ""
	if len(viewedRunID) > 0 {
		runID = viewedRunID[0]
	} else {
		var viewed apiJourney
		getJSONInto(t, base+"/api/journeys/"+key, &viewed)
		if len(viewed.Attempts) > 0 {
			runID = viewed.Attempts[0].RunID
		}
	}
	payload, _ := json.Marshal(map[string]string{"runId": runID, "verdict": verdict, "note": note})
	resp, err := http.Post(base+"/api/journeys/"+key+"/outcome", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST journey outcome: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, buf.Bytes()
}

// cleanupJourneyDecision removes a journey outcome after a test.
func cleanupJourneyDecision(t *testing.T, st *store.Store, key string) {
	t.Helper()
	t.Cleanup(func() {
		// Include helpers in other test files that use the current viewed attempt.
		decisions, err := st.ListDecisions(context.Background(), nil)
		if err != nil {
			t.Errorf("list cleanup decisions: %v", err)
			return
		}
		prefix := store.JourneyOutcomeDecisionID(key)
		for _, d := range decisions {
			if d.ID == prefix || strings.HasPrefix(d.ID, prefix+":") {
				_ = st.DeleteDecision(context.Background(), d.ID)
			}
		}
	})
}

// TestJourneyJoinsContinuationChain asserts a continuation is a chapter of its source delivery.
// Lineage comes from frozen launch options, not a legacy repair marker.
func TestJourneyJoinsContinuationChain(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)

	original := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "interrupted")
	continued := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")
	markContinuedFrom(t, st, continued, original)

	j := oneJourney(t, ts.URL, project)
	if j.AttemptCount != 2 {
		t.Fatalf("attempts = %d, want 2 (the original and its continuation)", j.AttemptCount)
	}
	if j.Attempts[0].RunID != continued || j.Attempts[1].RunID != original {
		t.Errorf("attempts = %s,%s — want the continuation first (newest)", j.Attempts[0].RunID, j.Attempts[1].RunID)
	}
	if !j.Attempts[1].Superseded {
		t.Error("the source attempt must be superseded")
	}
	if j.Attempts[0].ContinuedFrom != original {
		t.Errorf("continuedFrom = %q, want %q", j.Attempts[0].ContinuedFrom, original)
	}
	if j.Root != original {
		t.Errorf("root = %q, want the original %q", j.Root, original)
	}
	if !j.NeedsDecision || j.Stage != string(journey.StageNeedsDecision) {
		t.Errorf("stage = %q needsDecision = %v, want needs_decision/true", j.Stage, j.NeedsDecision)
	}
}

// TestJourneyGroupsFreshAttempts asserts repeated instantiations of one recipe
// are one delivery, while a different recipe is a different delivery.
func TestJourneyGroupsFreshAttempts(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)

	first := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")
	second := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")
	other := seedJourneyRun(t, st, project, "ship-api", "recipe-docs", "failed")

	list := listJourneys(t, ts.URL, project)
	if len(list) != 2 {
		t.Fatalf("got %d journeys, want 2 (one per recipe)", len(list))
	}
	byRecipe := map[string]apiJourney{}
	for _, j := range list {
		byRecipe[j.Recipe] = j
	}
	api := byRecipe["recipe-api"]
	if api.AttemptCount != 2 {
		t.Errorf("recipe-api attempts = %d, want 2", api.AttemptCount)
	}
	if api.Attempts[0].RunID != second || api.Attempts[1].RunID != first {
		t.Errorf("attempts out of order: %+v", api.Attempts)
	}
	if byRecipe["recipe-docs"].AttemptCount != 1 || byRecipe["recipe-docs"].Attempts[0].RunID != other {
		t.Errorf("recipe-docs journey is wrong: %+v", byRecipe["recipe-docs"])
	}
	// The key is derived, not stored — deriving twice must agree.
	if again := oneOf(listJourneys(t, ts.URL, project), "recipe-api"); again.Key != api.Key {
		t.Errorf("journey key is not stable across reads: %q then %q", api.Key, again.Key)
	}
}

// TestJourneyKeyMatchesDerivation pins the documented key formula: the API's key
// is exactly what journey.Key computes from the delivery identity.
func TestJourneyKeyMatchesDerivation(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	runID := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")

	j := oneJourney(t, ts.URL, project)
	if want := journey.Key(project, "ship-api", "recipe-api", runID); j.Key != want {
		t.Errorf("key = %q, want %q", j.Key, want)
	}
	if !journey.ValidKey(j.Key) {
		t.Errorf("derived key %q is not a valid key", j.Key)
	}
}

// TestGetJourneyByKey asserts the single-journey route returns the same
// projection the list does, and 400/404 for keys that cannot resolve.
func TestGetJourneyByKey(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")

	listed := oneJourney(t, ts.URL, project)
	var fetched apiJourney
	getJSONInto(t, ts.URL+"/api/journeys/"+listed.Key, &fetched)
	if fetched.Key != listed.Key || fetched.AttemptCount != listed.AttemptCount || fetched.Stage != listed.Stage {
		t.Errorf("by-key journey disagrees with the list:\n by key: %+v\n listed: %+v", fetched, listed)
	}

	if code := statusOf(t, ts.URL+"/api/journeys/not-a-key"); code != http.StatusBadRequest {
		t.Errorf("malformed key: got %d, want 400", code)
	}
	unknown := journey.Key("nobody", "nothing", "no-recipe", "no-run")
	if code := statusOf(t, ts.URL+"/api/journeys/"+unknown); code != http.StatusNotFound {
		t.Errorf("unknown key: got %d, want 404", code)
	}
}

// TestJourneyOutcomeThenConflict is the journey-grain outcome contract: the
// first verdict is recorded (201), a second is refused (409) with the original
// echoed back, and the decision row references every attempt.
func TestJourneyOutcomeThenConflict(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	first := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")
	second := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")

	j := oneJourney(t, ts.URL, project)
	cleanupJourneyDecision(t, st, j.Key)

	status, body := postJourneyOutcome(t, ts.URL, j.Key, "accepted", "good enough to ship", j.Attempts[0].RunID)
	if status != http.StatusCreated {
		t.Fatalf("first outcome: got %d, want 201: %s", status, body)
	}
	var created struct {
		Key        string `json:"key"`
		RunID      string `json:"runId"`
		Verdict    string `json:"verdict"`
		Note       string `json:"note"`
		Scope      string `json:"scope"`
		DecisionID string `json:"decisionId"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Key != j.Key || created.RunID != second || created.Verdict != "accepted" || created.Note != "good enough to ship" {
		t.Errorf("unexpected body: %+v", created)
	}
	if created.Scope != "journey" {
		t.Errorf("scope = %q, want journey", created.Scope)
	}
	if want := store.JourneyAttemptOutcomeDecisionID(j.Key, second); created.DecisionID != want {
		t.Errorf("decisionId = %q, want %q", created.DecisionID, want)
	}

	status, body = postJourneyOutcome(t, ts.URL, j.Key, "rejected", "changed my mind", j.Attempts[0].RunID)
	if status != http.StatusConflict {
		t.Fatalf("second outcome: got %d, want 409: %s", status, body)
	}
	var conflict struct {
		Outcome struct {
			Verdict string `json:"verdict"`
			RunID   string `json:"runId"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Outcome.Verdict != "accepted" || conflict.Outcome.RunID != second {
		t.Errorf("409 must echo the original verdict, got %q", conflict.Outcome.Verdict)
	}

	// The decision row carries the project and points at every attempt.
	d, err := st.GetDecision(context.Background(), store.JourneyAttemptOutcomeDecisionID(j.Key, second))
	if err != nil {
		t.Fatalf("get journey decision: %v", err)
	}
	if d.Project != project {
		t.Errorf("project = %q, want %q", d.Project, project)
	}
	if !hasString(d.Tags, store.OutcomeTag) || !hasString(d.Tags, "journey") {
		t.Errorf("tags = %v, want the outcome and journey tags", d.Tags)
	}
	for _, ref := range []string{"journey:" + j.Key, "run:" + first, "run:" + second} {
		if !hasString(d.Refs, ref) {
			t.Errorf("refs %v missing %q", d.Refs, ref)
		}
	}
}

// TestJourneyOutcomeValidation covers the refusals: a key that could not have
// been derived, an unknown verdict, and a delivery still being built.
func TestJourneyOutcomeValidation(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)

	if status, body := postJourneyOutcome(t, ts.URL, "not-a-key", "accepted", "", "no-run"); status != http.StatusBadRequest {
		t.Errorf("bad key: got %d, want 400: %s", status, body)
	}

	seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")
	j := oneJourney(t, ts.URL, project)
	if status, body := postJourneyOutcome(t, ts.URL, j.Key, "looks-good", "", j.Attempts[0].RunID); status != http.StatusBadRequest {
		t.Errorf("bad verdict: got %d, want 400: %s", status, body)
	}

	unknown := journey.Key("nobody", "nothing", "no-recipe", "no-run")
	if status, body := postJourneyOutcome(t, ts.URL, unknown, "accepted", "", "no-run"); status != http.StatusNotFound {
		t.Errorf("unknown journey: got %d, want 404: %s", status, body)
	}

	// A delivery with an attempt still running cannot be judged yet.
	active := newProject(t)
	seedJourneyRun(t, st, active, "ship-api", "recipe-api", "running")
	aj := oneJourney(t, ts.URL, active)
	if aj.Stage != string(journey.StageBuilding) {
		t.Fatalf("stage = %q, want building", aj.Stage)
	}
	if status, body := postJourneyOutcome(t, ts.URL, aj.Key, "accepted", "", aj.Attempts[0].RunID); status != http.StatusConflict {
		t.Errorf("active delivery: got %d, want 409: %s", status, body)
	}
}

// TestInboxAsksOncePerJourney is the point of the whole feature: several failed
// attempts at one product are ONE question, not one per run, and answering it
// clears the card.
func TestInboxAsksOncePerJourney(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)

	var runs []string
	for i := 0; i < 3; i++ {
		runs = append(runs, seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed"))
	}

	cards := inboxJourneysFor(t, ts.URL, project)
	if len(cards) != 1 {
		t.Fatalf("three attempts produced %d inbox cards, want 1", len(cards))
	}
	card := cards[0]
	if card.AttemptCount != 3 {
		t.Errorf("the card lists %d attempts, want 3", card.AttemptCount)
	}
	// Every superseded attempt is listed but none of them asks separately.
	for i, a := range card.Attempts {
		if i > 0 && !a.Superseded {
			t.Errorf("attempt %s should be superseded", a.RunID)
		}
	}
	if card.Attempts[0].RunID != runs[2] {
		t.Errorf("newest attempt = %s, want %s", card.Attempts[0].RunID, runs[2])
	}

	cleanupJourneyDecision(t, st, card.Key)
	if status, body := postJourneyOutcome(t, ts.URL, card.Key, "changes_requested", "needs a real gate", card.Attempts[0].RunID); status != http.StatusCreated {
		t.Fatalf("record outcome: %d: %s", status, body)
	}
	if left := inboxJourneysFor(t, ts.URL, project); len(left) != 0 {
		t.Errorf("a decided delivery still asks for %d decisions", len(left))
	}
}

// TestInboxDropsSupersededContinuedAttempt asserts a run continued after interruption stops
// asking for its own verdict — the delivery asks once, through its newest
// attempt.
func TestInboxDropsSupersededContinuedAttempt(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)

	original := seedJourneyRun(t, st, project, "ship-api", "", "interrupted")
	continued := seedJourneyRun(t, st, project, "ship-api", "", "failed")
	markContinuedFrom(t, st, continued, original)

	cards := inboxJourneysFor(t, ts.URL, project)
	if len(cards) != 1 {
		t.Fatalf("got %d inbox cards, want 1 — a continuation is not a second delivery", len(cards))
	}
	if cards[0].Attempts[0].RunID != continued {
		t.Errorf("the card should ask about the continuation (%s), got %s", continued, cards[0].Attempts[0].RunID)
	}
}

// TestInboxSkipsStoppedDelivery asserts cancelling a run is itself the decision.
func TestInboxSkipsStoppedDelivery(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	seedJourneyRun(t, st, project, "ship-api", "recipe-api", "interrupted")

	if j := oneJourney(t, ts.URL, project); j.Stage != string(journey.StageStopped) || j.NeedsDecision {
		t.Errorf("stage = %q needsDecision = %v, want stopped/false", j.Stage, j.NeedsDecision)
	}
	if cards := inboxJourneysFor(t, ts.URL, project); len(cards) != 0 {
		t.Errorf("a cancelled delivery queued %d decisions, want 0", len(cards))
	}
}

// TestRunDetailEmbedsJourney asserts the per-run Delivery tab can tell which
// delivery it is a chapter of, and see a journey-level verdict, from the fetch
// it already makes.
func TestRunDetailEmbedsJourney(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	first := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")
	second := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")

	var detail struct {
		Journey *struct {
			Key          string `json:"key"`
			Stage        string `json:"stage"`
			Maturity     string `json:"maturity"`
			AttemptCount int    `json:"attemptCount"`
			Attempt      int    `json:"attempt"`
			Superseded   bool   `json:"superseded"`
			Outcome      *struct {
				Verdict string `json:"verdict"`
				Scope   string `json:"scope"`
			} `json:"outcome"`
		} `json:"journey"`
	}
	getJSONInto(t, ts.URL+"/api/runs/"+first, &detail)
	if detail.Journey == nil {
		t.Fatal("run detail carries no journey")
	}
	if detail.Journey.AttemptCount != 2 || detail.Journey.Attempt != 1 || !detail.Journey.Superseded {
		t.Errorf("older attempt: %+v — want attempt 1 of 2, superseded", detail.Journey)
	}
	if detail.Journey.Outcome != nil {
		t.Errorf("expected no outcome yet, got %+v", detail.Journey.Outcome)
	}
	key := detail.Journey.Key
	cleanupJourneyDecision(t, st, key)

	getJSONInto(t, ts.URL+"/api/runs/"+second, &detail)
	if detail.Journey.Attempt != 2 || detail.Journey.Superseded {
		t.Errorf("newest attempt: %+v — want attempt 2 of 2, not superseded", detail.Journey)
	}
	if detail.Journey.Key != key {
		t.Errorf("the two attempts report different journeys: %q vs %q", key, detail.Journey.Key)
	}
	if detail.Journey.Maturity != string(journey.MaturityUnproven) {
		t.Errorf("maturity = %q, want unproven (ordinary task completion is not a check)", detail.Journey.Maturity)
	}

	if status, body := postJourneyOutcome(t, ts.URL, key, "accepted", "", second); status != http.StatusCreated {
		t.Fatalf("record outcome: %d: %s", status, body)
	}
	// Both chapters now show the journey-level verdict.
	for _, runID := range []string{first, second} {
		getJSONInto(t, ts.URL+"/api/runs/"+runID, &detail)
		if detail.Journey.Outcome == nil || detail.Journey.Outcome.Verdict != "accepted" ||
			detail.Journey.Outcome.Scope != "journey" {
			t.Errorf("run %s: journey outcome = %+v, want an accepted journey-scoped verdict", runID, detail.Journey.Outcome)
		}
		if detail.Journey.Maturity != string(journey.MaturityAccepted) {
			t.Errorf("run %s: maturity = %q, want accepted", runID, detail.Journey.Maturity)
		}
	}
}

func TestRunDetailKeepsAttemptThreeWhenFiveExist(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	var runs []string
	for i := 0; i < 5; i++ {
		runs = append(runs, seedJourneyRun(t, st, project, "ship-five", "recipe-five", "failed"))
	}
	var detail struct {
		Journey *struct {
			AttemptCount int  `json:"attemptCount"`
			Attempt      int  `json:"attempt"`
			Superseded   bool `json:"superseded"`
		} `json:"journey"`
	}
	getJSONInto(t, ts.URL+"/api/runs/"+runs[2], &detail)
	if detail.Journey == nil || detail.Journey.Attempt != 3 || detail.Journey.AttemptCount != 5 || !detail.Journey.Superseded {
		t.Fatalf("attempt 3 header payload = %+v, want attempt 3 of 5 superseded", detail.Journey)
	}
}

// TestRunOutcomeStillRecordableButNotDemanded asserts the run-grain endpoint
// survives as an optional annotation: it still records, and recording it on the
// current attempt answers the delivery's question too.
func TestRunOutcomeStillRecordableButNotDemanded(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	runID := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")

	if cards := inboxJourneysFor(t, ts.URL, project); len(cards) != 1 {
		t.Fatalf("got %d cards before deciding, want 1", len(cards))
	}
	if status, body := postOutcome(t, ts.URL+"/api/runs/"+runID+"/outcome", "accepted", "fine"); status != http.StatusCreated {
		t.Fatalf("record run outcome: %d: %s", status, body)
	}
	if cards := inboxJourneysFor(t, ts.URL, project); len(cards) != 0 {
		t.Errorf("a run-grain verdict on the current attempt left %d cards, want 0", len(cards))
	}
	j := oneJourney(t, ts.URL, project)
	if j.Outcome == nil || j.Outcome.Scope != "run" || j.Outcome.RunID != runID {
		t.Errorf("effective outcome = %+v, want the run-grain annotation", j.Outcome)
	}
	if j.Stage != string(journey.StageAccepted) {
		t.Errorf("stage = %q, want accepted", j.Stage)
	}
}

// Journey checks come from this run's frozen request and committed checkpoint.
func TestJourneyCountsOnlyConfiguredSDKChecks(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)

	runID := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed", "c1", "c2")
	if _, err := st.Pool.Exec(t.Context(), `UPDATE runs SET doc=jsonb_set(doc,'{sdk,nodes,2,phase}','"failed"'::jsonb) WHERE run_id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	child := seedChildRun(t, st, runID, "ordinary-child", "passed")
	seedNode(t, st, child, "c2", "gate", "passed", "")

	j := oneJourney(t, ts.URL, project)
	if j.Criteria != 2 || j.Proven != 1 {
		t.Errorf("criteria = %d/%d proven, want 1/2 counted from the frozen SDK declaration and checkpoint", j.Proven, j.Criteria)
	}
	if j.Attempts[0].Criteria != 2 || j.Attempts[0].Proven != 1 {
		t.Errorf("chapter tally = %d/%d, want 1/2", j.Attempts[0].Proven, j.Attempts[0].Criteria)
	}
	if j.Maturity != string(journey.MaturityProvenOnce) {
		t.Errorf("maturity = %q, want proven_once", j.Maturity)
	}
}

// TestJourneyTimelineIsNarrative asserts the journey page has a story to tell
// and nothing to click: events carry text and a run link, and no verdict field
// that would make it a second inbox.
func TestJourneyTimelineIsNarrative(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	seedJourneyRun(t, st, project, "ship-api", "recipe-api", "failed")

	j := oneJourney(t, ts.URL, project)
	if len(j.Timeline) == 0 {
		t.Fatal("journey has no timeline")
	}
	for _, e := range j.Timeline {
		if e.Text == "" {
			t.Errorf("timeline event %q has no text", e.Kind)
		}
	}
	raw := rawJSON(t, ts.URL+"/api/journeys/"+j.Key)
	var probe struct {
		Timeline []map[string]any `json:"timeline"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	for _, e := range probe.Timeline {
		for _, banned := range []string{"verdict", "action", "actions", "decision"} {
			if _, ok := e[banned]; ok {
				t.Errorf("timeline event carries %q — journey events must be non-actionable", banned)
			}
		}
	}
}

// Freeze source identity exactly as SDK continuation admission does. This is a
// persisted-fixture helper, not a runtime continuation or replay operation.
func markContinuedFrom(t *testing.T, st *store.Store, child, source string) {
	t.Helper()
	origin, err := st.GetRun(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]string{"sourceRunId": source, "rootRunId": store.SDKDeliveryRoot(origin)})
	if _, err := st.Pool.Exec(t.Context(), `UPDATE runs SET parent_run=$2, options=jsonb_set(options,'{continuation}',$3::jsonb) WHERE run_id=$1`, child, source, metadata); err != nil {
		t.Fatal(err)
	}
}

// inboxJourneysFor returns the inbox's journey cards for one project.
func inboxJourneysFor(t *testing.T, base, project string) []apiJourney {
	t.Helper()
	var inbox struct {
		Outcomes []apiJourney `json:"outcomes"`
	}
	getJSONInto(t, base+"/api/inbox", &inbox)
	var out []apiJourney
	for _, j := range inbox.Outcomes {
		if j.Project == project {
			out = append(out, j)
		}
	}
	return out
}

// oneOf picks the journey with a given recipe out of a list.
func oneOf(list []apiJourney, recipe string) apiJourney {
	for _, j := range list {
		if j.Recipe == recipe {
			return j
		}
	}
	return apiJourney{}
}

// statusOf GETs a URL and returns only the status code.
func statusOf(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestJourneyStaysQuietWhileContinuationRuns(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	source := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "interrupted")
	child := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "running")
	markContinuedFrom(t, st, child, source)
	if cards := inboxJourneysFor(t, ts.URL, project); len(cards) != 0 {
		t.Fatalf("running continuation produced %d decision cards", len(cards))
	}
	j := oneJourney(t, ts.URL, project)
	if j.NeedsDecision || j.Stage != string(journey.StageBuilding) || j.Attempts[0].ContinuedFrom != source {
		t.Fatalf("active continuation=%+v", j)
	}
	found := false
	for _, event := range j.Timeline {
		if event.RunID == child && event.Kind == "attempt_started" {
			found = strings.Contains(event.Text, "continued")
		}
		if event.Kind == "self_healed" || event.Kind == "sub_repaired" {
			t.Fatalf("invented automatic repair: %+v", event)
		}
	}
	if !found {
		t.Fatalf("timeline lost continued attempt: %+v", j.Timeline)
	}
}

// rawJSON GETs a URL and returns the raw body.
func rawJSON(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return buf.Bytes()
}

func TestJourneyRequiresExplicitTaskVerificationAndIgnoresOrdinaryCompletion(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	runID := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")
	j := oneJourney(t, ts.URL, project)
	if j.Criteria != 0 || j.Proven != 0 || j.Maturity != string(journey.MaturityUnproven) {
		t.Fatalf("ordinary completion invented evidence: %+v", j)
	}
	// A configured task check needs the host's verification metadata, while a
	// separately configured model review uses its committed review verdict.
	nodes := []map[string]any{
		{"id": "draft", "title": "Write files", "kind": "task"},
		{"id": "measure", "title": "Check measurements", "kind": "task", "check": []string{"sh", "checks.sh"}},
		{"id": "review", "title": "Review the output", "kind": "check"},
	}
	states := []map[string]any{
		{"id": "draft", "phase": "passed", "verification": "not_requested"},
		{"id": "measure", "phase": "passed", "verification": "pending"},
		{"id": "review", "phase": "passed"},
	}
	options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": map[string]any{"workflow": "node-workflow", "nodes": nodes}})
	write := func() {
		t.Helper()
		checkpoint, _ := json.Marshal(map[string]any{"phase": "passed", "nodes": states})
		if _, err := st.Pool.Exec(t.Context(), `UPDATE runs SET options=$2, doc=jsonb_set(doc,'{sdk}',$3::jsonb) WHERE run_id=$1`, runID, options, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	write()
	j = oneJourney(t, ts.URL, project)
	if j.Criteria != 2 || j.Proven != 1 {
		t.Fatalf("unverified task counted as checked: %+v", j)
	}
	states[1]["verification"] = "passed"
	write()
	j = oneJourney(t, ts.URL, project)
	if j.Criteria != 2 || j.Proven != 2 {
		t.Fatalf("committed task verification omitted: %+v", j)
	}
}

func TestJourneyContinuationNeedsItsOwnImmutableOutcome(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	source := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "interrupted")
	viewed := oneJourney(t, ts.URL, project)
	cleanupJourneyDecision(t, st, viewed.Key)
	status, body := postJourneyOutcome(t, ts.URL, viewed.Key, "changes_requested", "finish the review", source)
	if status != http.StatusCreated {
		t.Fatalf("source decision: %d %s", status, body)
	}
	firstID := store.JourneyAttemptOutcomeDecisionID(viewed.Key, source)
	before, err := st.GetDecision(t.Context(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	continued := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")
	markContinuedFrom(t, st, continued, source)
	current := oneJourney(t, ts.URL, project)
	if current.Key != viewed.Key || current.Outcome != nil || !current.NeedsDecision {
		t.Fatalf("prior outcome covered continuation: %+v", current)
	}
	cards := inboxJourneysFor(t, ts.URL, project)
	if len(cards) != 1 || cards[0].Attempts[0].RunID != continued {
		t.Fatalf("continuation not awaiting review: %+v", cards)
	}
	status, body = postJourneyOutcome(t, ts.URL, current.Key, "accepted", "reviewed continued result", continued)
	if status != http.StatusCreated {
		t.Fatalf("continuation decision: %d %s", status, body)
	}
	secondID := store.JourneyAttemptOutcomeDecisionID(current.Key, continued)
	after, err := st.GetDecision(t.Context(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatalf("prior immutable decision changed: before=%s after=%s", beforeJSON, afterJSON)
	}
	second, err := st.GetDecision(t.Context(), secondID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == firstID || second.Decision != "accepted" || second.Why != "reviewed continued result" {
		t.Fatalf("continuation decision: %+v", second)
	}
	current = oneJourney(t, ts.URL, project)
	if current.Outcome == nil || current.Outcome.Verdict != "accepted" || current.Outcome.RunID != continued || current.NeedsDecision {
		t.Fatalf("latest accepted outcome: outcome=%+v journey=%+v", current.Outcome, current)
	}
	if cards := inboxJourneysFor(t, ts.URL, project); len(cards) != 0 {
		t.Fatalf("decided continuation asks again: %+v", cards)
	}
	status, body = postJourneyOutcome(t, ts.URL, current.Key, "rejected", "replace accepted", continued)
	if status != http.StatusConflict {
		t.Fatalf("duplicate attempt decision: %d %s", status, body)
	}
}

func TestJourneyOutcomeRejectsStaleAndMissingViewedAttempt(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	project := newProject(t)
	source := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "interrupted")
	viewed := oneJourney(t, ts.URL, project)
	child := seedJourneyRun(t, st, project, "ship-api", "recipe-api", "passed")
	markContinuedFrom(t, st, child, source)
	cleanupJourneyDecision(t, st, viewed.Key)
	if status, body := postJourneyOutcome(t, ts.URL, viewed.Key, "accepted", "stale page", source); status != http.StatusConflict {
		t.Fatalf("stale viewed attempt: %d %s", status, body)
	}
	// postOutcome sends only verdict/note, modeling an older client with no runId.
	if status, body := postOutcome(t, ts.URL+"/api/journeys/"+viewed.Key+"/outcome", "accepted", ""); status != http.StatusBadRequest {
		t.Fatalf("missing viewed attempt: %d %s", status, body)
	}
	for _, id := range []string{store.JourneyOutcomeDecisionID(viewed.Key), store.JourneyAttemptOutcomeDecisionID(viewed.Key, source), store.JourneyAttemptOutcomeDecisionID(viewed.Key, child)} {
		if _, err := st.GetDecision(t.Context(), id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("refused request wrote decision %s: %v", id, err)
		}
	}
	current := oneJourney(t, ts.URL, project)
	if current.Outcome != nil || !current.NeedsDecision {
		t.Fatalf("refused request altered outcome: %+v", current)
	}
}
