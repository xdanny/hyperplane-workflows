package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// openStore connects to the same database the test server uses, so a test can
// seed rows the HTTP API then acts on.
func openStore(t *testing.T) *store.Store {
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
	return st
}

func postJSON(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte("{}")))
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

// TestExtractSkillRejectsIneligibleRun asserts the endpoint refuses to learn
// from a run that did not pass — learning stays downstream of a verified result.
func TestExtractSkillRejectsIneligibleRun(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()

	runID := fmt.Sprintf("run-extract-http-%d", time.Now().UnixNano())
	if err := st.CreateRun(ctx, store.Run{RunID: runID, DAG: "hello", State: "failed"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })

	status, body := postJSON(t, ts.URL+"/api/runs/"+runID+"/extract-skill")
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for a failed run, got %d: %s", status, body)
	}

	status, _ = postJSON(t, ts.URL+"/api/runs/no-such-run/extract-skill")
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown run, got %d", status)
	}
}

// TestApproveRejectSkillEndpoints walks a draft skill through approval and a
// second draft through rejection.
func TestApproveRejectSkillEndpoints(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()

	draftID := fmt.Sprintf("draft-http-%d", time.Now().UnixNano())
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: draftID, Name: "Draft method", Instruction: "Do the method.",
		Metadata: json.RawMessage(`{"status":"draft","sourceRun":"run-x"}`),
	}); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), draftID) })
	// A LEARNED draft needs a practice test before it may enter prompts —
	// TestApproveLearnedSkillWithoutEvalset covers the refusal itself.
	seedSkillEvalset(t, st, draftID)

	status, body := postJSON(t, ts.URL+"/api/skills/"+draftID+"/approve")
	if status != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", status, body)
	}
	var approved store.Skill
	if err := json.Unmarshal(body, &approved); err != nil {
		t.Fatalf("decode approved skill: %v", err)
	}
	if store.IsDraftSkill(approved) {
		t.Errorf("approve must clear the draft marker, metadata=%s", approved.Metadata)
	}

	// Re-approving is a conflict — the draft is already gone from the queue.
	if status, _ := postJSON(t, ts.URL+"/api/skills/"+draftID+"/approve"); status != http.StatusConflict {
		t.Errorf("second approve: expected 409, got %d", status)
	}
	if status, _ := postJSON(t, ts.URL+"/api/skills/unknown-skill/approve"); status != http.StatusNotFound {
		t.Errorf("unknown skill: expected 404, got %d", status)
	}

	rejectID := fmt.Sprintf("reject-http-%d", time.Now().UnixNano())
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: rejectID, Name: "Rejectable", Instruction: "x",
		Metadata: json.RawMessage(`{"status":"draft"}`),
	}); err != nil {
		t.Fatalf("seed reject draft: %v", err)
	}
	if status, body := postJSON(t, ts.URL+"/api/skills/"+rejectID+"/reject"); status != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d: %s", status, body)
	}
	if _, err := st.GetSkill(ctx, rejectID); !errors.Is(err, store.ErrNotFound) {
		t.Error("a rejected draft must be deleted")
	}
}

// seedSkillEvalset links a practice test to a skill — the entry ticket a
// learned draft needs before approval will let it into prompts.
func seedSkillEvalset(t *testing.T, st *store.Store, skillID string) {
	t.Helper()
	if err := st.UpsertEvalset(context.Background(), store.Evalset{
		ID:       "evalset-" + skillID,
		Name:     "Practice test for " + skillID,
		SkillRef: skillID,
		Cases: []store.EvalCase{{
			ID:       "case-1",
			Input:    json.RawMessage(`{"subject":"an unrelated subject"}`),
			Criteria: "applies the method to a subject the run never touched",
		}},
	}); err != nil {
		t.Fatalf("seed evalset for %q: %v", skillID, err)
	}
}

// TestApproveLearnedSkillWithoutEvalset walks the guardrail over HTTP: a draft
// the platform learned is refused with 409 until an evalset names it, the body
// says why and what to do, and the draft is untouched by the refusal. A
// hand-written draft — nobody's automated proposal — is exempt.
func TestApproveLearnedSkillWithoutEvalset(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()

	learned := fmt.Sprintf("learned-noeval-%d", time.Now().UnixNano())
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: learned, Name: "Learned method", Instruction: "Do the method.",
		Metadata: json.RawMessage(`{"status":"draft","sourceRun":"run-x","sourceRuns":["run-x"],"supportRuns":1}`),
	}); err != nil {
		t.Fatalf("seed learned draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), learned) })

	status, body := postJSON(t, ts.URL+"/api/skills/"+learned+"/approve")
	if status != http.StatusConflict {
		t.Fatalf("approve without an evalset: expected 409, got %d: %s", status, body)
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	for _, want := range []string{"practice test", "hyperplane eval gen " + learned} {
		if !strings.Contains(errBody.Error, want) {
			t.Errorf("the refusal must be actionable — %q missing from %q", want, errBody.Error)
		}
	}
	after, err := st.GetSkill(ctx, learned)
	if err != nil {
		t.Fatalf("get skill after refusal: %v", err)
	}
	if !store.IsDraftSkill(after) {
		t.Error("a refused approval must leave the skill a draft")
	}

	// The same call succeeds once the skill has a practice test.
	seedSkillEvalset(t, st, learned)
	if status, body := postJSON(t, ts.URL+"/api/skills/"+learned+"/approve"); status != http.StatusOK {
		t.Fatalf("approve with an evalset: expected 200, got %d: %s", status, body)
	}

	// Hand-written drafts are not held behind the gate: the judgement it
	// stands in for was supplied by whoever wrote the instruction.
	handwritten := fmt.Sprintf("hand-noeval-%d", time.Now().UnixNano())
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: handwritten, Name: "Hand-written", Instruction: "I wrote this.",
		Metadata: json.RawMessage(`{"status":"draft"}`),
	}); err != nil {
		t.Fatalf("seed hand-written draft: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), handwritten) })
	if status, body := postJSON(t, ts.URL+"/api/skills/"+handwritten+"/approve"); status != http.StatusOK {
		t.Fatalf("hand-written approve: expected 200, got %d: %s", status, body)
	}
}
