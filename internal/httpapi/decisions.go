package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleListDecisions returns decision records (newest first), optionally scoped
// to a project via ?project=. Absent returns all decisions; an empty value
// (?project=) returns only unscoped decisions; ?project=x scopes to x.
// GET /api/decisions[?project=hyperplane]
func (s *Server) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDecisions(r.Context(), projectFilter(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if items == nil {
		items = []store.Decision{}
	}
	// Named, not raw: a ledger row says which workflow and which run of it, so
	// a reader never has to recognise `journey:j-871c…` (S-13).
	writeJSON(w, http.StatusOK, s.describeDecisions(r.Context(), items))
}

// The projects surface (list/get/create/patch) lives in projects.go.

// handleGetDecision returns one decision by id.
// GET /api/decisions/{id}
func (s *Server) handleGetDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid decision id")))
		return
	}
	d, err := s.store.GetDecision(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleCreateDecision upserts a decision record.
// POST /api/decisions  body: {id?, title, status?, context?, decision, why?, ...}
// id is generated (slug of title) when omitted.
func (s *Server) handleCreateDecision(w http.ResponseWriter, r *http.Request) {
	var body store.Decision
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Title == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'title' is required")))
		return
	}
	if body.ID == "" {
		body.ID = slugify(body.Title)
	}
	out, err := s.store.UpsertDecision(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// handleDeleteDecision removes a decision record by id.
// DELETE /api/decisions/{id}
func (s *Server) handleDeleteDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid decision id")))
		return
	}
	if err := s.store.DeleteDecision(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errBody(err))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// slugify turns a decision title into a stable lowercase id, falling back to
// "decision" when nothing slug-able is left.
func slugify(s string) string { return slugifyOr(s, "decision") }

// slugifyOr turns a title into a stable lowercase id, returning fallback when
// the title contains nothing slug-able. Callers that have no sensible default
// pass "" and treat the empty result as "the caller must supply an id".
func slugifyOr(s, fallback string) string {
	var b []rune
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b = append(b, r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b = append(b, r+('a'-'A'))
			prevDash = false
		default:
			if !prevDash && len(b) > 0 {
				b = append(b, '-')
				prevDash = true
			}
		}
	}
	out := string(b)
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if out == "" {
		return fallback
	}
	return out
}
