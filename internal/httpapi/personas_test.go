package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/persona"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// seedPersona registers a charter directly in the store, the way `make seed`
// would, so the API tests act on a real registered reviewer.
func seedPersona(t *testing.T, st *store.Store, id string) {
	t.Helper()
	charter := "# " + id + " — test reviewer\n\n" +
		"## IDENTITY\n\nYou are a reviewer.\n\nYour bar: **\"Does it hold up?\"**\n\n" +
		"## RULES\n\n1. **Exact quotes required.**\n\n" +
		"## AUDIT PROTOCOL\n\n1. Read the evidence.\n\n" +
		"## COMPETENCE\n\n- **Skills**: `ai-engineering`.\n\n" +
		"## SKILL-REVIEW PROTOCOL\n\nVerdict: APPROVE, REJECT, REVISE.\n"
	err := st.UpsertPersona(context.Background(), store.Persona{
		ID: id, Name: id, Bar: "Does it hold up?", Charter: charter,
		Competence: json.RawMessage(`{"skills":["ai-engineering"]}`),
		Version:    "testhash", Source: "personas/" + id + ".md",
	})
	if err != nil {
		t.Fatalf("seed persona: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM personas WHERE id=$1`, id)
	})
}

// getJSON GETs a URL and returns the raw body plus the status, so a test can
// assert on a refusal as readily as on a result.
func getJSON(t *testing.T, url string) ([]byte, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.Bytes(), resp.StatusCode
}

func postBody(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
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

// TestPersonaRegistryEndpoints covers the read side: the roster lists what is
// registered, the detail view carries the charter itself, and an unregistered
// id is a 404 that says how to fix it.
func TestPersonaRegistryEndpoints(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	id := fmt.Sprintf("persona-http-%d", time.Now().UnixNano())
	seedPersona(t, st, id)

	body, status := getJSON(t, ts.URL+"/api/personas")
	if status != http.StatusOK {
		t.Fatalf("list personas: %d: %s", status, body)
	}
	var roster []struct {
		ID         string `json:"id"`
		Bar        string `json:"bar"`
		Reviews    int    `json:"reviews"`
		Charter    string `json:"charter"`
		Competence struct {
			Skills []string `json:"skills"`
		} `json:"competence"`
	}
	if err := json.Unmarshal(body, &roster); err != nil {
		t.Fatalf("decode roster: %v: %s", err, body)
	}
	found := false
	for _, p := range roster {
		if p.ID != id {
			continue
		}
		found = true
		if p.Bar == "" {
			t.Error("the roster must carry the reviewer's own bar")
		}
		if p.Charter != "" {
			t.Error("the list response must not carry the full charter — the detail view does")
		}
		if len(p.Competence.Skills) != 1 {
			t.Errorf("competence.skills = %v", p.Competence.Skills)
		}
	}
	if !found {
		t.Fatalf("registered persona %q is missing from the roster", id)
	}

	body, status = getJSON(t, ts.URL+"/api/personas/"+id)
	if status != http.StatusOK {
		t.Fatalf("get persona: %d: %s", status, body)
	}
	if !strings.Contains(string(body), "SKILL-REVIEW PROTOCOL") {
		t.Error("the detail view must carry the charter in full")
	}

	body, status = getJSON(t, ts.URL+"/api/personas/no-such-persona")
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for an unregistered persona, got %d: %s", status, body)
	}
	if !strings.Contains(string(body), "make seed") {
		t.Errorf("the 404 should say how to register personas: %s", body)
	}
}

// TestPersonaCompetenceRouting covers the hook the AI-12 auto-trigger will use:
// a persona is claimed for a skill only when their charter NAMED it, and the
// answer says why. A bad target is refused rather than guessed.
func TestPersonaCompetenceRouting(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	id := fmt.Sprintf("persona-route-%d", time.Now().UnixNano())
	seedPersona(t, st, id)

	body, status := getJSON(t, ts.URL+"/api/personas?competentFor=skill:ai-engineering")
	if status != http.StatusOK {
		t.Fatalf("competentFor: %d: %s", status, body)
	}
	var matched []struct {
		ID           string `json:"id"`
		CompetentFor string `json:"competentFor"`
	}
	if err := json.Unmarshal(body, &matched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	hit := false
	for _, m := range matched {
		if m.ID == id {
			hit = true
			if m.CompetentFor == "" {
				t.Error("a routing answer must say on what grounds it matched")
			}
		}
	}
	if !hit {
		t.Fatalf("the persona that named `ai-engineering` was not matched: %s", body)
	}

	// A skill nobody named: this persona must not be claimed for it.
	body, _ = getJSON(t, ts.URL+"/api/personas?competentFor=skill:something-nobody-named")
	if strings.Contains(string(body), id) {
		t.Errorf("competence must never be inferred: %s", body)
	}

	_, status = getJSON(t, ts.URL+"/api/personas?competentFor=browser:localhost")
	if status != http.StatusBadRequest {
		t.Errorf("an unknown target kind must be a 400, got %d", status)
	}
}

// TestPersonaReviewValidation covers every refusal that happens BEFORE the
// billed engine call: an unregistered persona, a malformed target, and a target
// that names something that does not exist. None of these may spend a call.
func TestPersonaReviewValidation(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	id := fmt.Sprintf("persona-review-%d", time.Now().UnixNano())
	seedPersona(t, st, id)

	status, body := postBody(t, ts.URL+"/api/personas/no-such-persona/review", `{"target":"skill:x"}`)
	if status != http.StatusNotFound {
		t.Fatalf("unregistered persona: expected 404, got %d: %s", status, body)
	}

	for _, target := range []string{"", "run", "browser:localhost", "skill:../etc/passwd"} {
		payload, _ := json.Marshal(map[string]string{"target": target})
		status, body = postBody(t, ts.URL+"/api/personas/"+id+"/review", string(payload))
		if status != http.StatusBadRequest {
			t.Errorf("target %q: expected 400, got %d: %s", target, status, body)
		}
	}

	status, body = postBody(t, ts.URL+"/api/personas/"+id+"/review", `{"target":"run:no-such-run"}`)
	if status != http.StatusNotFound {
		t.Errorf("unknown run: expected 404, got %d: %s", status, body)
	}
	status, body = postBody(t, ts.URL+"/api/personas/"+id+"/review", `{"target":"skill:no-such-skill"}`)
	if status != http.StatusNotFound {
		t.Errorf("unknown skill: expected 404, got %d: %s", status, body)
	}
}

// TestPersonaReviewSurvivesClientDisconnect is the detached-context contract:
// a review is a billed engine call that runs for minutes, so a client that
// times out or hangs up mid-call (the CLI's old 10s default did exactly this)
// must not cancel it — the call finishes and the verdict is still recorded.
func TestPersonaReviewSurvivesClientDisconnect(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	st := openStore(t)
	suffix := time.Now().UnixNano()
	personaID := fmt.Sprintf("persona-detach-%d", suffix)
	seedPersona(t, st, personaID)

	skillID := fmt.Sprintf("skill-detach-%d", suffix)
	if err := st.UpsertSkill(context.Background(), store.Skill{
		ID: skillID, Name: "Detach test skill", Instruction: "Do the thing.",
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), skillID) })

	// The fake engine holds the call open until the client is provably gone,
	// then reports whether its context survived and returns a verdict.
	engineStarted := make(chan struct{})
	clientGone := make(chan struct{})
	engineCtxErr := make(chan error, 1)
	srv.SetPersonaCodex(func(ctx context.Context, _ codex.Options) (codex.Result, error) {
		close(engineStarted)
		<-clientGone
		engineCtxErr <- ctx.Err()
		b, _ := json.Marshal(map[string]any{
			"verdict": "approve", "summary": "holds up",
			"reasons": []string{"survived the disconnect"},
		})
		return codex.Result{OK: true, JSON: b, Text: string(b)}, nil
	})

	reqCtx, hangUp := context.WithCancel(context.Background())
	defer hangUp()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		ts.URL+"/api/personas/"+personaID+"/review",
		strings.NewReader(fmt.Sprintf(`{"target":"skill:%s"}`, skillID)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		reqDone <- err
	}()

	select {
	case <-engineStarted:
	case <-time.After(15 * time.Second):
		t.Fatal("the engine call never started")
	}
	hangUp() // the client gives up mid-call, exactly like a client-side timeout
	if err := <-reqDone; err == nil {
		t.Fatal("expected the hung-up request to return an error")
	}
	// Give the server a moment to notice the dead connection — without the
	// detached context, this is what would cancel the engine's ctx.
	time.Sleep(500 * time.Millisecond)
	close(clientGone)

	if err := <-engineCtxErr; err != nil {
		t.Fatalf("the client disconnect cancelled the billed engine call: %v", err)
	}

	// The verdict must still have been recorded, though nobody was listening.
	recID := persona.ReviewID(personaID, persona.Target{Kind: persona.TargetSkill, ID: skillID}, 1)
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, recID) })
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec, err := st.GetRecord(context.Background(), store.PersonaReviewRecordType, recID)
		if err == nil {
			rev, perr := persona.ParseReview(rec.Doc)
			if perr != nil {
				t.Fatalf("recorded review does not parse: %v", perr)
			}
			if rev.Verdict != persona.VerdictApprove {
				t.Fatalf("verdict = %q, want %q", rev.Verdict, persona.VerdictApprove)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the verdict was never recorded after the disconnect: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestPersonaReviewsListing asserts recorded reviews are readable by target and
// that several reviews of one target are all returned — the AI-12 retention
// rule, seen from the surface that renders them.
func TestPersonaReviewsListing(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()
	target := fmt.Sprintf("skill:listed-%d", time.Now().UnixNano())

	for i, verdict := range []string{"revise", "approve"} {
		recID := fmt.Sprintf("persona-review-someone-%s-%d", strings.ReplaceAll(target, ":", "-"), i+1)
		doc, _ := json.Marshal(map[string]any{
			"id": recID, "persona": "someone", "personaName": "Someone",
			"target": target, "targetKind": "skill", "targetId": strings.TrimPrefix(target, "skill:"),
			"verdict": verdict, "summary": "verdict " + verdict, "advisory": true,
		})
		if err := st.ClaimRecord(ctx, store.Record{
			Type: store.PersonaReviewRecordType, ID: recID, State: verdict,
			Kind: "persona-review", Consumed: true, Doc: doc,
		}); err != nil {
			t.Fatalf("seed review: %v", err)
		}
		t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, recID) })
	}

	body, status := getJSON(t, ts.URL+"/api/persona-reviews?target="+target)
	if status != http.StatusOK {
		t.Fatalf("list reviews: %d: %s", status, body)
	}
	var reviews []struct {
		ID       string `json:"id"`
		Verdict  string `json:"verdict"`
		Advisory bool   `json:"advisory"`
	}
	if err := json.Unmarshal(body, &reviews); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if len(reviews) != 2 {
		t.Fatalf("both reviews of a target must be returned, got %d: %s", len(reviews), body)
	}
	for _, r := range reviews {
		if !r.Advisory {
			t.Error("every review must report itself as advisory")
		}
	}

	_, status = getJSON(t, ts.URL+"/api/persona-reviews?target=nonsense")
	if status != http.StatusBadRequest {
		t.Errorf("a malformed target filter must be a 400, got %d", status)
	}
}

func TestPersonaRunReviewUsesFrozenSDKCheckClassification(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	st := openStore(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	personaID, runID := "persona-sdk-"+suffix, "run-persona-sdk-"+suffix
	seedPersona(t, st, personaID)
	run := store.Run{RunID: runID, DAG: "node-workflow", State: "passed",
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"write","title":"Write files","kind":"task"},{"id":"measure","title":"Check data","kind":"task","check":["sh","checks.sh"]},{"id":"review","title":"Review meaning","kind":"check"}]}}`),
		Doc:     store.JSON(`{"sdk":{"phase":"passed","nodes":[{"id":"write","phase":"passed","verification":"not_requested"},{"id":"measure","phase":"passed","verification":"passed"},{"id":"review","phase":"passed"}]}}`)}
	if err := st.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })
	prompt := make(chan string, 1)
	srv.SetPersonaCodex(func(_ context.Context, options codex.Options) (codex.Result, error) {
		prompt <- options.Instruction
		body := []byte(`{"verdict":"revise","summary":"Separate execution from correctness","reasons":["Model review remains fallible"]}`)
		return codex.Result{OK: true, JSON: body, Text: string(body)}, nil
	})
	status, body := postBody(t, ts.URL+"/api/personas/"+personaID+"/review", fmt.Sprintf(`{"target":"run:%s"}`, runID))
	if status != http.StatusOK {
		t.Fatalf("review status=%d: %s", status, body)
	}
	recordID := persona.ReviewID(personaID, persona.Target{Kind: persona.TargetRun, ID: runID}, 1)
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, recordID) })
	got := <-prompt
	// The last string used to be "File CONTENTS are not included", a blanket
	// caution from when no file ever reached a reviewer. Documents DO reach it
	// now (S-15, D-14), so this run — whose folder holds nothing — must say
	// that it could read nothing, rather than implying a rule that no longer
	// holds for runs that did produce documents.
	for _, want := range []string{"step write (Write files): phase=passed; no configured check", "deterministic check; check passed=true", "model review (fallible); check passed=true", "No document this run produced could be read"} {
		if !strings.Contains(got, want) {
			t.Errorf("review prompt omitted %q: %s", want, got)
		}
	}
	if strings.Contains(got, "verdict-bearing nodes") || strings.Contains(got, "sub-run=") {
		t.Fatal("review still uses a graph proof scaffold")
	}
}

// A run page asks "what has anyone said about THIS run?". It should not have to
// know how a target string is spelled to ask it.
func TestPersonaReviewsFilterByRun(t *testing.T) {
	ts := setupTestServer(t)
	st := openStore(t)
	ctx := context.Background()
	runID := fmt.Sprintf("20260101-%d-node-workflow", time.Now().UnixNano())
	target := "run:" + runID

	recID := "persona-review-someone-run-" + fmt.Sprint(time.Now().UnixNano())
	doc, _ := json.Marshal(map[string]any{
		"id": recID, "persona": "someone", "personaName": "Someone",
		"target": target, "targetKind": "run", "targetId": runID,
		"verdict": "revise", "summary": "the claim about growth is unsupported", "advisory": true,
	})
	if err := st.ClaimRecord(ctx, store.Record{Type: store.PersonaReviewRecordType, ID: recID,
		State: "revise", Kind: "persona-review", Consumed: true, Doc: doc}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteRecord(context.Background(), store.PersonaReviewRecordType, recID) })

	body, status := getJSON(t, ts.URL+"/api/persona-reviews?run="+runID)
	if status != http.StatusOK {
		t.Fatalf("list reviews by run: %d: %s", status, body)
	}
	var reviews []struct {
		ID      string `json:"id"`
		Target  string `json:"target"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(body, &reviews); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if len(reviews) != 1 || reviews[0].Target != target || reviews[0].Verdict != "revise" {
		t.Fatalf("run filter returned %d review(s): %s", len(reviews), body)
	}

	// Another run's page must not show this one's review.
	body, status = getJSON(t, ts.URL+"/api/persona-reviews?run=20260101-000000-other")
	if status != http.StatusOK {
		t.Fatalf("list reviews for another run: %d: %s", status, body)
	}
	if err := json.Unmarshal(body, &reviews); err != nil || len(reviews) != 0 {
		t.Fatalf("another run must have no reviews: %s", body)
	}
	if _, status = getJSON(t, ts.URL+"/api/persona-reviews?run=../escape"); status != http.StatusBadRequest {
		t.Fatalf("an invalid run id must be refused, got %d", status)
	}
	if _, status = getJSON(t, ts.URL+"/api/persona-reviews?run="+runID+"&target=skill:other"); status != http.StatusBadRequest {
		t.Fatalf("two different filters must be refused, got %d", status)
	}
}
