package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleListSkills returns skills, optionally filtered by ?q= and ?limit=.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var limit int
	if l := r.URL.Query().Get("limit"); l != "" {
		limit, _ = strconv.Atoi(l)
	}
	skills, err := s.store.SearchSkills(r.Context(), q, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if skills == nil {
		skills = []store.Skill{}
	}
	// The registry carries the same Approve button the inbox does, so it needs
	// the same warning about what a draft was learned from — see
	// markRehearsalProvenance.
	writeJSON(w, http.StatusOK, s.markRehearsalProvenance(r.Context(), skills))
}

// handleGetSkill returns a single skill by id.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sk, err := s.store.GetSkill(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

// handleUpsertSkill creates or updates a skill from the request body.
func (s *Server) handleUpsertSkill(w http.ResponseWriter, r *http.Request) {
	var sk store.Skill
	if err := json.NewDecoder(r.Body).Decode(&sk); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if sk.ID == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'id' is required")))
		return
	}
	if err := s.store.UpsertSkill(r.Context(), sk); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	// Re-fetch to return the stored record with updated_at.
	result, err := s.store.GetSkill(r.Context(), sk.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"id": sk.ID})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleApproveSkill handles POST /api/skills/{id}/approve — the human step of
// the extraction pipeline. It flips a draft to active; only then can a workflow
// node resolve the skill.
//
// A draft the platform LEARNED is refused with 409 until an evalset names it:
// approval is what puts an instruction into every prompt that resolves the
// skill, and a proposal backed only by runs that passed has never been tested
// on anything. The body carries the sentence and the command that fixes it, so
// the refusal is actionable in the Inbox, the registry, and the terminal alike.
func (s *Server) handleApproveSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sk, err := knowledge.ApproveSkill(r.Context(), s.store, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("skill %q not found", id)))
	case errors.Is(err, knowledge.ErrNotDraft), errors.Is(err, knowledge.ErrNoEvalset),
		errors.Is(err, knowledge.ErrRehearsalSource):
		writeJSON(w, http.StatusConflict, errBody(err))
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errBody(err))
	default:
		writeJSON(w, http.StatusOK, sk)
	}
}

// handleRejectSkill handles POST /api/skills/{id}/reject — discards a draft.
// Only drafts can be rejected; an approved skill is deleted deliberately.
func (s *Server) handleRejectSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := knowledge.RejectSkill(r.Context(), s.store, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("skill %q not found", id)))
	case errors.Is(err, knowledge.ErrNotDraft):
		writeJSON(w, http.StatusConflict, errBody(err))
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errBody(err))
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "rejected"})
	}
}

// handleExtractSkill handles POST /api/runs/{id}/extract-skill — proposes the
// reusable method a passed run demonstrates and stores it as a draft skill.
// Returns the draft, or the duplicate verdict when the base already covers it.
func (s *Server) handleExtractSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runDir, err := safeRunDir(s.cfg.RunsDir, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}

	res, err := knowledge.ExtractSkill(r.Context(), s.store, knowledge.Extractor{
		SchemasDir: filepath.Join(s.cfg.PlatformRoot, "schemas"),
		TargetRoot: s.cfg.TargetRoot,
		RunDir:     runDir,
	}, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("run %q not found", id)))
	case errors.Is(err, knowledge.ErrRunNotEligible):
		writeJSON(w, http.StatusConflict, errBody(err))
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errBody(err))
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
