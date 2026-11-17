package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleInbox returns everything waiting on a human: pending approval and
// selection records (decisions a run is blocked on), finished runs with no
// Outcome Decision yet (deliveries nobody has signed off), and the skills the
// platform proposed to itself after an acceptance (drafts, inert until
// approved). One unified queue — mid-run gates, end-of-run verdicts and
// "should I keep what I learned?" are all "your turn".
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	approvals, err := s.store.ListRecords(r.Context(), "approvals", "pending")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	selections, err := s.store.ListRecords(r.Context(), "selections", "pending")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if approvals == nil {
		approvals = []store.Record{}
	}
	if selections == nil {
		selections = []store.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approvals":  s.describeApprovals(r.Context(), approvals),
		"selections": selections,
		"outcomes":   s.describeOutcomes(r.Context(), s.listOutcomesNeeded(r.Context())),
		"proposals":  s.listSkillProposals(r.Context()),
	})
}

// handleApprovalDecision processes an approval decision.
// Body: {decision: "approved"|"rejected"}
func (s *Server) handleApprovalDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Decision != "approved" && body.Decision != "rejected" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("decision must be 'approved' or 'rejected'")))
		return
	}

	rec, err := s.store.GetRecord(r.Context(), "approvals", id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	if rec.Kind == "sdk-approval" {
		decided, err := s.store.DecideSDKApproval(r.Context(), id, body.Decision)
		if errors.Is(err, store.ErrSDKApprovalExpired) {
			writeJSON(w, 409, errBody(err))
			return
		}
		if err != nil {
			writeJSON(w, 500, errBody(err))
			return
		}
		writeJSON(w, 200, decided)
		return
	}

	// Merge decision into doc and update state.
	doc := mergeDocField(rec.Doc, "decision", body.Decision)
	rec.State = body.Decision
	rec.Doc = doc

	if err := s.store.UpsertRecord(r.Context(), rec); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSelectionDecision processes a selection gate decision.
// Body: {winner, winnerCandidate?}
func (s *Server) handleSelectionDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Winner          string `json:"winner"`
		WinnerCandidate string `json:"winnerCandidate,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Winner == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'winner' is required")))
		return
	}

	rec, err := s.store.GetRecord(r.Context(), "selections", id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	// Merge winner into doc and mark as approved.
	doc := mergeDocField(rec.Doc, "winner", body.Winner)
	if body.WinnerCandidate != "" {
		doc = mergeDocField(doc, "winnerCandidate", body.WinnerCandidate)
	}
	rec.State = "approved"
	rec.Doc = doc

	if err := s.store.UpsertRecord(r.Context(), rec); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	// Self-feeding knowledge: a human selection IS a decision — mirror it into the
	// decisions table so it's searchable + graphable. Best-effort.
	s.mineSelectionDecision(r.Context(), id, rec, body.Winner)

	writeJSON(w, http.StatusOK, rec)
}

// safeSlug makes an id path-safe (so the mined decision is addressable via
// GET/DELETE, which path-guard ids) by replacing any char outside [A-Za-z0-9._-].
func safeSlug(s string) string {
	b := []rune(s)
	for i, r := range b {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			b[i] = '-'
		}
	}
	return string(b)
}

// mineSelectionDecision records an approved selection as an ADR-style decision.
func (s *Server) mineSelectionDecision(ctx context.Context, selID string, rec store.Record, winner string) {
	var doc struct {
		Title     string `json:"title"`
		Prompt    string `json:"prompt"`
		Criterion string `json:"criterion"`
		GateNode  string `json:"gateNode"`
		Dag       string `json:"dag"`
	}
	_ = json.Unmarshal(rec.Doc, &doc)

	title := doc.Title
	if title == "" {
		if doc.GateNode != "" {
			title = "Selection · " + doc.GateNode
		} else {
			title = "Selection " + selID
		}
	}
	ctxText := doc.Prompt
	if ctxText == "" {
		ctxText = doc.Criterion
	}
	d := store.Decision{
		ID:       "sel-" + safeSlug(selID),
		Title:    title,
		Status:   "accepted",
		Context:  ctxText,
		Decision: "Selected: " + winner,
		Tags:     []string{"selection"},
		Source:   "run:" + rec.RunID,
	}
	if _, err := s.store.UpsertDecision(ctx, d); err != nil {
		log.Printf("mine selection decision %s: %v", selID, err)
	}
}

// handleListPreferences returns preference signals for a skill.
func (s *Server) handleListPreferences(w http.ResponseWriter, r *http.Request) {
	skill := r.PathValue("skill")
	prefs, err := s.store.ListPreferences(r.Context(), skill)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if prefs == nil {
		prefs = []store.Preference{}
	}
	writeJSON(w, http.StatusOK, prefs)
}

// handleDismissRecord dismisses a single pending inbox record (approval or selection).
func (s *Server) handleDismissRecord(w http.ResponseWriter, r *http.Request) {
	recType := r.PathValue("recordType")
	id := r.PathValue("id")
	if recType != "approvals" && recType != "selections" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("recordType must be 'approvals' or 'selections'")))
		return
	}
	// Map URL segment to record type stored in DB
	storeType := "approvals"
	if recType == "selections" {
		storeType = "selections"
	}
	if err := s.store.DismissRecord(r.Context(), storeType, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errBody(err))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

// handleClearStale dismisses all pending records whose run is no longer blocked/running.
func (s *Server) handleClearStale(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.DismissStaleRecords(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": n})
}

// mergeDocField parses a JSON object, sets key=value, and re-serialises.
// If doc is nil or invalid, it starts from an empty object.
func mergeDocField(doc json.RawMessage, key, value string) json.RawMessage {
	m := make(map[string]any)
	if len(doc) > 0 {
		_ = json.Unmarshal(doc, &m)
	}
	m[key] = value
	b, _ := json.Marshal(m)
	return b
}
