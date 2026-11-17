package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleListRouting handles GET /api/routing.
func (s *Server) handleListRouting(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListRoutingPolicies(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if list == nil {
		list = []store.RoutingPolicy{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRoutingEval handles POST /api/routing/eval.
// Body: { skill: "id", evalset: "evalsetId", margin: 0.1 }
// Runs the evalset with both local and codex engines, compares, upserts policy.
func (s *Server) handleRoutingEval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Skill   string  `json:"skill"`
		Evalset string  `json:"evalset"`
		Margin  float64 `json:"margin"`
	}
	body.Margin = 0.1 // default
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	result, err := eval.EvaluateRouting(r.Context(), s.store, body.Skill, body.Evalset, body.Margin, s.cfg.TargetRoot)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, eval.ErrInvalidRoutingEvaluation):
			status = http.StatusBadRequest
		case errors.Is(err, store.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
			status = http.StatusNotFound
		case errors.Is(err, store.ErrRoutingEvaluationChanged):
			status = http.StatusConflict
		case errors.Is(err, eval.ErrNoPassingRoutingEngine):
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleUpsertRouting handles POST /api/routing.
// Body: { scope: "skill:foo", engine: "codex-local", note: "..." }
func (s *Server) handleUpsertRouting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope  string  `json:"scope"`
		Engine string  `json:"engine"`
		Note   string  `json:"note"`
		Margin float64 `json:"margin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Scope == "" || body.Engine == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("scope and engine are required")))
		return
	}

	p := store.RoutingPolicy{
		Scope:  body.Scope,
		Engine: body.Engine,
		Note:   body.Note,
	}
	if body.Margin != 0 {
		p.Margin = &body.Margin
	}
	if err := s.store.UpsertRoutingPolicy(r.Context(), p); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}
