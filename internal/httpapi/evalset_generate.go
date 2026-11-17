package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Approving a learned skill is blocked until the skill has a practice test, and
// the only way to make one was a terminal command. That made the whole learning
// loop read as theater from the UI: every skill showed no evalset, and the one
// remedy was off-screen. This endpoint runs the SAME generator the CLI runs —
// one implementation in internal/optimize, not a second copy — so a person can
// create the practice test where they are being asked for it.
//
// It is a billed engine call, so it is deliberate: one skill at a time, never
// on a schedule, and an existing evalset is not replaced unless the caller says
// so in as many words.

// generateEvalsetTimeout bounds the engine call. It matches the codex default
// the CLI generator already runs under.
const generateEvalsetTimeout = 10 * time.Minute

func (s *Server) handleGenerateEvalset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid skill id")))
		return
	}
	var body struct {
		Engine  string `json:"engine"`
		Replace bool   `json:"replace"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	// The platform's own engine is the default: the local model is an opt-in for
	// working offline, not the thing a UI button should quietly depend on.
	if body.Engine == "" {
		body.Engine = "codex"
	}
	if body.Engine != "codex" && body.Engine != "local" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("engine must be %q or %q", "codex", "local")))
		return
	}
	if _, err := s.store.GetSkill(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("skill %q is not registered", id)))
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	existing, err := optimize.EvalsetForSkill(r.Context(), s.store, id)
	switch {
	case err == nil && !body.Replace:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "this skill already has a practice test — pass replace:true to generate a new one over it",
			"evalsetId": existing,
		})
		return
	case err != nil && !errors.Is(err, optimize.ErrNoEvalset):
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	// The engine call outlives the request the way a persona review does: a
	// person who closes the tab should not cancel a call they are paying for.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), generateEvalsetTimeout)
	defer cancel()
	evalset, err := optimize.GenerateEvalset(ctx, s.store, id, body.Engine)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errBody(fmt.Errorf("could not generate a practice test: %w", err)))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"evalsetId": evalset.ID, "cases": evalset.Cases})
}
