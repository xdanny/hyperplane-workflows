package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/orchestrate"
	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handlePostOrchestrate starts checked SDK backlog tasks asynchronously and
// returns the orchestrate run ID immediately.
// POST /api/orchestrate
// Body: {backlog:[{title,detail,check:argv}], maxItems, dryRun, targetRoot, stopOnFail, autoRepair, project}
func (s *Server) handlePostOrchestrate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Backlog    []orchestrate.BacklogItem `json:"backlog"`
		MaxItems   int                       `json:"maxItems"`
		DryRun     bool                      `json:"dryRun"`
		Engine     string                    `json:"engine"`
		TargetRoot string                    `json:"targetRoot"`
		StopOnFail bool                      `json:"stopOnFail"`
		AutoRepair bool                      `json:"autoRepair"`
		Project    string                    `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if len(body.Backlog) == 0 {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("backlog must not be empty")))
		return
	}

	targetRoot := body.TargetRoot
	if targetRoot == "" {
		targetRoot = s.cfg.TargetRoot
	}

	cfg := orchestrate.OrchestrateConfig{
		BacklogItems: body.Backlog, MaxItems: body.MaxItems, TargetRoot: targetRoot,
		Engine: body.Engine, DryRun: body.DryRun, StopOnFail: body.StopOnFail,
		AutoRepair: body.AutoRepair, Project: body.Project, RunsDir: s.cfg.RunsDir,
		Execute: s.runner.RunCheckedTask,
	}
	if err := orchestrate.Validate(r.Context(), cfg); err != nil {
		writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
		return
	}
	orchID := sdkruntime.NewRunID("backlog")
	if _, err := s.store.CreateOrchestrateRun(r.Context(), store.CreateOrchestrateRunParams{
		ID: orchID, BacklogSize: len(body.Backlog), MaxItems: body.MaxItems,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	persist := func(result orchestrate.OrchestrateResult, status string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		items, _ := json.Marshal(result.Items)
		return s.store.UpdateOrchestrateRun(ctx, orchID, result.Done, result.Failed, items, status)
	}
	cfg.Progress = func(result orchestrate.OrchestrateResult) error { return persist(result, "running") }
	if err := s.runner.launch(orchID, nil, func(ctx context.Context) {
		result, err := orchestrate.Orchestrate(ctx, s.store, cfg)
		status := "done"
		if err != nil || result.Failed > 0 {
			status = "failed"
		}
		if ctx.Err() != nil {
			status = "interrupted"
		}
		_ = persist(result, status)
	}); err != nil {
		_ = persist(orchestrate.OrchestrateResult{}, "failed")
		writeJSON(w, http.StatusServiceUnavailable, errBody(err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"id": orchID})
}

// handleListOrchestrate returns all orchestrate_runs (newest first).
// GET /api/orchestrate
func (s *Server) handleListOrchestrate(w http.ResponseWriter, r *http.Request) {
	recs, err := s.store.ListOrchestrateRuns(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

// handleGetOrchestrate returns the state of a single orchestrate_runs record.
// GET /api/orchestrate/:id
func (s *Server) handleGetOrchestrate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid orchestrate run id")))
		return
	}
	rec, err := s.store.GetOrchestrateRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// Cancellation belongs to the managed batch; child SDK sessions inherit it.
func (s *Server) handleCancelOrchestrate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid backlog id")))
		return
	}
	if _, err := s.store.GetOrchestrateRun(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, errBody(err))
		return
	}
	s.runner.Cancel(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
