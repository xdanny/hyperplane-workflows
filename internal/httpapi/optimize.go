package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleOptimizeSkill handles POST /api/skills/{id}/optimize.
// Body (all optional): { variants: N, apply: bool, evalset: "id",
//
//	proposer: "local"|"codex", score_engine: "local"|"codex", min_gain: 0.0 }
//
// Returns the full optimize.Result as JSON.
func (s *Server) handleOptimizeSkill(w http.ResponseWriter, r *http.Request) {
	skillID := r.PathValue("id")

	// Validate the skill exists.
	if _, err := s.store.GetSkill(r.Context(), skillID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("skill %q not found", skillID)))
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	var body struct {
		Variants    int     `json:"variants"`
		Apply       bool    `json:"apply"`
		Evalset     string  `json:"evalset"`
		Proposer    string  `json:"proposer"`
		ScoreEngine string  `json:"score_engine"`
		MinGain     float64 `json:"min_gain"`
	}
	// Body is optional — defaults apply if missing.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
			return
		}
	}

	result, err := optimize.Optimize(r.Context(), s.store, optimize.Config{
		SkillID:     skillID,
		EvalsetID:   body.Evalset,
		Variants:    body.Variants,
		Proposer:    body.Proposer,
		ScoreEngine: body.ScoreEngine,
		MinGain:     body.MinGain,
		Apply:       body.Apply,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleListOptimizeRuns handles GET /api/skills/{id}/optimize-runs.
func (s *Server) handleListOptimizeRuns(w http.ResponseWriter, r *http.Request) {
	skillID := r.PathValue("id")
	runs, err := optimize.ListOptimizeRuns(r.Context(), s.store, skillID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if runs == nil {
		runs = []optimize.OptimizeRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}
