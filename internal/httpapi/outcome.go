package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// The Outcome Decision closes the loop a run opens: the workflow proves what it
// can with gates and verifies, and the human records what that proof was worth.
// It is stored as a decision record so an outcome is searchable and graphable
// next to every other decision, and it is INSERT-ONLY: an outcome is final.
// Changing your mind means deleting the decision (DELETE /api/decisions/{id}),
// which leaves a deliberate, visible act rather than a silently rewritten
// verdict.

const (
	outcomeAccepted         = "accepted"
	outcomeChangesRequested = "changes_requested"
	outcomeRejected         = "rejected"
)

// outcomeDecider names who recorded a verdict. Hyperplane is local-first with
// no auth — one human at one keyboard — so a person's decision is "local"
// rather than an identity we cannot verify. An acceptance the server recorded
// itself, under a workflow's sign-off policy, says so instead: it is labelled
// automatic wherever it appears and is never read as a person's judgment.
func outcomeDecider(d store.Decision) string {
	if d.DecidedBy == "" {
		return store.DecidedByLocal
	}
	return d.DecidedBy
}

// maxOutcomeNote caps the free-text note so a paste cannot balloon a row.
const maxOutcomeNote = 4000

// maxOutcomeInbox bounds the "needs decision" queue so the inbox poll stays cheap.
const maxOutcomeInbox = 50

// runOutcome is the wire shape of a recorded Outcome Decision. It is embedded in
// the run detail response so the Delivery view knows, in one fetch, whether the
// run has been decided on.
type runOutcome struct {
	RunID      string    `json:"runId"`
	Verdict    string    `json:"verdict"`
	Note       string    `json:"note,omitempty"`
	DecidedBy  string    `json:"decidedBy"`
	DecidedAt  time.Time `json:"decidedAt"`
	DecisionID string    `json:"decisionId"`
}

// validOutcomeVerdict reports whether v is one of the three recordable verdicts.
func validOutcomeVerdict(v string) bool {
	return v == outcomeAccepted || v == outcomeChangesRequested || v == outcomeRejected
}

// terminalRunState reports whether a run has finished and can therefore be
// judged. Deciding on a still-running run would be judging an unfinished
// product.
func terminalRunState(state string) bool {
	return state == "passed" || state == "failed" || state == "interrupted"
}

// outcomeFromDecision projects a stored decision row onto the outcome wire shape.
func outcomeFromDecision(runID string, d store.Decision) runOutcome {
	at := d.CreatedAt
	if d.DecidedAt != nil {
		at = *d.DecidedAt
	}
	return runOutcome{
		RunID:      runID,
		Verdict:    d.Decision,
		Note:       d.Why,
		DecidedBy:  outcomeDecider(d),
		DecidedAt:  at,
		DecisionID: d.ID,
	}
}

// lookupRunOutcome returns the run's recorded outcome, or nil when there is
// none. A store error is treated as "none" on purpose: the outcome is an
// enrichment of the run detail response, and failing to read it must not fail
// the whole run fetch.
func (s *Server) lookupRunOutcome(ctx context.Context, runID string) *runOutcome {
	d, err := s.store.GetRunOutcome(ctx, runID)
	if err != nil {
		return nil
	}
	o := outcomeFromDecision(runID, d)
	return &o
}

// handleRecordOutcome records the human's final call on a finished run.
// POST /api/runs/{id}/outcome  body: {verdict: accepted|changes_requested|rejected, note?}
//
// 201 on success, 409 when an outcome already exists (the existing one is
// returned in the body) or when the run has not finished yet.
func (s *Server) handleRecordOutcome(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}

	var body struct {
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	verdict := strings.TrimSpace(body.Verdict)
	if !validOutcomeVerdict(verdict) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf(
			"'verdict' must be one of %q, %q, %q", outcomeAccepted, outcomeChangesRequested, outcomeRejected)))
		return
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > maxOutcomeNote {
		note = note[:maxOutcomeNote]
	}

	run, err := s.store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("run %q not found", id)))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if !terminalRunState(run.State) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("run is %s — an outcome can only be recorded once the run has finished", run.State),
			"state": run.State,
		})
		return
	}
	// A rehearsal executed nothing: its agent nodes answered with stand-ins and
	// its approval gates simulated their own verdicts. ACCEPTING it would put a
	// human signature on results that do not exist — and, downstream, hand skill
	// extraction a "delivery you accepted" to learn a method from. The other two
	// verdicts stay open: saying a rehearsal is not good enough, or asking for
	// changes to what it planned, are both honest things to record.
	if verdict == outcomeAccepted && store.IsRehearsal(run) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     store.RehearsalSentence,
			"mode":      run.Mode,
			"rehearsal": true,
		})
		return
	}

	now := time.Now().UTC()
	d, err := s.store.CreateDecision(r.Context(), store.Decision{
		ID:        store.OutcomeDecisionID(id),
		Title:     "Outcome · " + runLabel(run),
		Status:    "accepted",
		Context:   outcomeContext(run),
		Decision:  verdict,
		Why:       note,
		Tags:      []string{store.OutcomeTag, verdict},
		Refs:      []string{"run:" + id},
		Project:   run.Project,
		Source:    "run:" + id,
		DecidedAt: &now,
	})
	if errors.Is(err, store.ErrExists) {
		// An outcome is final. Surface the one already on record rather than
		// silently overwriting a verdict someone else (or an earlier you) gave.
		existing := s.lookupRunOutcome(r.Context(), id)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "run already has an outcome — delete the decision to record a different verdict",
			"outcome": existing,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	// The verdict is now on record, which is the first moment this run's account
	// can include what a person decided about it. Fire-and-forget — see briefs.go.
	s.briefDeliveryDecision(id, d)

	writeJSON(w, http.StatusCreated, outcomeFromDecision(id, d))
}

// runLabel is the human name for a run: its goal when it has one, else its DAG.
func runLabel(run store.Run) string {
	if g := strings.TrimSpace(run.Goal); g != "" {
		return g
	}
	return run.DAG
}

// outcomeContext records what was being judged, so the decision reads on its own
// in the knowledge graph long after the run folder is gone.
func outcomeContext(run store.Run) string {
	ctx := fmt.Sprintf("Run %s (%s) finished %s.", run.RunID, run.DAG, run.State)
	if recipe := runRecipe(run); recipe != "" {
		ctx += " Recipe: " + recipe + "."
	}
	if g := strings.TrimSpace(run.Goal); g != "" {
		ctx += " Request: " + g
	}
	return ctx
}

// runDoc is the slice of a run's doc jsonb the delivery surfaces care about. A
// parallel recipe composer stamps `recipe`/`slots`/`criteria` onto instantiated
// runs; every other run simply has none of them, which is why each field is
// optional and nothing here fails on an absent or malformed doc.
type runDoc struct {
	Recipe   string `json:"recipe"`
	Criteria []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Hint  string `json:"hint"`
	} `json:"criteria"`
}

// parseRunDoc reads the recipe keys out of a run's doc, tolerating any shape.
func parseRunDoc(run store.Run) runDoc {
	var d runDoc
	if len(run.Doc) > 0 {
		_ = json.Unmarshal(run.Doc, &d)
	}
	return d
}

// runRecipe returns the recipe/template id a run was instantiated from, if any.
func runRecipe(run store.Run) string { return parseRunDoc(run).Recipe }

// listOutcomesNeeded builds the "needs decision" queue at JOURNEY grain: one
// card per delivery, listing its attempts, never one card per run.
//
// This is the change the whole feature exists for. Seven failed runs that are
// really two products used to be seven questions; now they are two, and each one
// carries its own history so the answer can be given from the card. Superseded
// attempts — repaired, or followed by a newer attempt of the same delivery —
// appear inside their journey but never ask for anything of their own.
//
// Best-effort: a failure here must not take down the approvals and selections
// the inbox primarily serves.
func (s *Server) listOutcomesNeeded(ctx context.Context) []journey.Journey {
	journeys, err := s.deriveJourneys(ctx, store.RunFilter{})
	if err != nil {
		return []journey.Journey{}
	}
	pending := journey.NeedingDecision(journeys)
	if len(pending) > maxOutcomeInbox {
		pending = pending[:maxOutcomeInbox]
	}
	return pending
}
