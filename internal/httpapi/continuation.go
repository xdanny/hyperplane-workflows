package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

var continuationToken = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *Server) handleReviewContinuation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	review, err := sdkruntime.ReviewContinuation(r.Context(), s.store, s.cfg.RunsDir, id)
	if err != nil {
		writeJSON(w, continuationErrorStatus(err), errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (s *Server) handleStartContinuation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	// The source definition and restart boundary are server-owned. The caller can
	// only acknowledge the exact review, never replace steps or choose an index.
	var body struct {
		ReviewToken string `json:"reviewToken"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid continuation request: %w", err)))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("provide one continuation request")))
		return
	}
	if !continuationToken.MatchString(body.ReviewToken) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("review this continuation before starting it")))
		return
	}
	runID, err := s.runner.StartContinuation(id, body.ReviewToken)
	if err != nil {
		var existing *store.SDKContinuationExists
		if errors.As(err, &existing) {
			writeJSON(w, http.StatusAccepted, map[string]string{"runId": existing.RunID})
			return
		}
		writeJSON(w, continuationErrorStatus(err), errBody(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"runId": runID})
}

func continuationErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, agentchecks.ErrValidationUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, store.ErrContinuationChanged), errors.Is(err, agentchecks.ErrContinuationBlocked), errors.Is(err, store.ErrRunActive):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
