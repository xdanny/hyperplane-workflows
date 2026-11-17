package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/improve"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handlePostImprove starts one self-improvement pass asynchronously and returns
// the improve run ID immediately.
// POST /api/improve
// Body: {skills:[], minFeedback:3, maxSkills:0, apply:false, engine:"", dryRun:false}
//
// An EMPTY body ("{}" — what the Skill Lab's "Run now" button sends) runs the
// same pass the timer runs, through the same non-overlap lock: clicking during
// a scheduled pass returns 409 rather than starting a second one over the same
// skills and evalsets. A body with options is still an explicit, bespoke pass
// and keeps its own configuration.
func (s *Server) handlePostImprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Skills      []string `json:"skills"`
		MinFeedback int      `json:"minFeedback"`
		MaxSkills   int      `json:"maxSkills"`
		Apply       bool     `json:"apply"`
		Engine      string   `json:"engine"`
		DryRun      bool     `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}

	// The work outlives the request: the response goes back as soon as the pass
	// has an id, so the pass must not run on a context that is cancelled the
	// moment that response is flushed.
	bg := context.WithoutCancel(r.Context())

	if running, trigger := s.improveLoop.Running(); running {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "a self-improvement pass is already running — wait for it to finish",
			"trigger": trigger,
		})
		return
	}

	// Generate a unique ID for this improve run.
	improveID := time.Now().Format("20060102-150405.000") + "-improve"

	// Persist the initial record so GET is immediately resolvable.
	initialRun := store.ImproveRun{
		ID:     improveID,
		Status: "running",
	}
	if _, err := s.store.CreateImproveRun(r.Context(), initialRun); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(fmt.Errorf("create improve run: %w", err)))
		return
	}

	cfg := improve.ImproveConfig{
		Skills:      body.Skills,
		MinFeedback: body.MinFeedback,
		MaxSkills:   body.MaxSkills,
		Apply:       body.Apply,
		Engine:      body.Engine,
		DryRun:      body.DryRun,
	}

	// Run asynchronously so the HTTP response returns immediately, holding the
	// same lock the scheduled pass takes.
	go func() {
		ran, _ := s.improveLoop.TryCycle(bg, "manual", func(ctx context.Context) error {
			result, err := improve.Improve(ctx, s.store, cfg)

			status := "done"
			if err != nil {
				status = "failed"
			}

			itemsJSON, _ := json.Marshal(result.Items)
			finalRun := store.ImproveRun{
				ID:               improveID,
				SkillsConsidered: result.SkillsConsidered,
				SkillsOptimized:  result.SkillsOptimized,
				SkillsPromoted:   result.SkillsPromoted,
				Items:            itemsJSON,
				Status:           status,
			}
			if uerr := s.store.UpdateImproveRun(ctx, finalRun); uerr != nil {
				log.Printf("improve %s: %v", improveID, uerr)
			}
			return err
		})
		if !ran {
			// A scheduled pass claimed the lock between the check above and
			// this call. The row is already persisted, so it must be closed
			// out — a record left saying "running" forever would read, from
			// the Skill Lab, as a pass that hung.
			log.Printf("improve %s: skipped — another pass claimed the loop", improveID)
			if uerr := s.store.UpdateImproveRun(bg, store.ImproveRun{
				ID: improveID, Status: "skipped", Items: json.RawMessage(`[]`),
			}); uerr != nil {
				log.Printf("improve %s: %v", improveID, uerr)
			}
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"id": improveID})
}

// handleListImprove returns all improve_runs, newest first.
// GET /api/improve
func (s *Server) handleListImprove(w http.ResponseWriter, r *http.Request) {
	recs, err := s.store.ListImproveRuns(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

// handleGetImprove returns the state of a single improve_runs record.
// GET /api/improve/{id}
func (s *Server) handleGetImprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid improve run id")))
		return
	}
	rec, err := s.store.GetImproveRun(r.Context(), id)
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
