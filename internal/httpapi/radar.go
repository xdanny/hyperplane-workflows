package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ── Tech Radar ─────────────────────────────────────────────────────────────

// handleListTechRadar returns all tech radar entries.
// GET /api/tech-radar
func (s *Server) handleListTechRadar(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListTechRadar(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if entries == nil {
		entries = []store.TechRadar{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleCreateTechRadar inserts a new tech radar entry.
// POST /api/tech-radar  body: {tech, quadrant, ring, note?}
func (s *Server) handleCreateTechRadar(w http.ResponseWriter, r *http.Request) {
	var body store.TechRadar
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Tech == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'tech' is required")))
		return
	}
	if body.Quadrant == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'quadrant' is required")))
		return
	}
	if body.Ring == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'ring' is required")))
		return
	}
	out, err := s.store.CreateTechRadar(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ── Stacks ─────────────────────────────────────────────────────────────────

// handleListStacks returns all stacks.
// GET /api/stacks
func (s *Server) handleListStacks(w http.ResponseWriter, r *http.Request) {
	stacks, err := s.store.ListStacks(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if stacks == nil {
		stacks = []store.Stack{}
	}
	writeJSON(w, http.StatusOK, stacks)
}

// handleCreateStack inserts a new stack.
// POST /api/stacks  body: {name, description?, runtime?, dockerized?, notes?, files?}
func (s *Server) handleCreateStack(w http.ResponseWriter, r *http.Request) {
	var body store.Stack
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'name' is required")))
		return
	}
	out, err := s.store.CreateStack(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ── Categories ─────────────────────────────────────────────────────────────

// handleListCategories returns all categories with live counts.
// GET /api/categories
func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.ListCategories(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if cats == nil {
		cats = []store.Category{}
	}
	writeJSON(w, http.StatusOK, cats)
}

// handleCreateCategory inserts a new category.
// POST /api/categories  body: {name, kind?}
func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var body store.Category
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'name' is required")))
		return
	}
	out, err := s.store.CreateCategory(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ── Workflow Templates ──────────────────────────────────────────────────────

// handleListTemplates returns all workflow templates.
// GET /api/templates
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	tmps, err := s.store.ListTemplates(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if tmps == nil {
		tmps = []store.WorkflowTemplate{}
	}
	// Describe outputs from the prepared SDK steps.
	s.describeRecipes(r.Context(), tmps)
	writeJSON(w, http.StatusOK, tmps)
}

// handleCreateTemplate inserts a new workflow template.
// POST /api/templates  body: {name, description?, spec}
func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var body store.WorkflowTemplate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'name' is required")))
		return
	}
	out, err := s.store.CreateTemplate(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// handleInstantiateTemplate prepares an unsaved SDK workflow from reviewed inputs.
// POST /api/templates/{id}/instantiate; run:true explicitly launches the result.
func (s *Server) handleInstantiateTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		Slots   map[string]string `json:"slots"`
		Run     bool              `json:"run"`
		DryRun  bool              `json:"dryRun"`
		Goal    string            `json:"goal"`
		Project string            `json:"project"`
		// AutoRepair opts this delivery into spending its one bounded repair
		// automatically on failure. Off unless the human asked for it.
		AutoRepair bool `json:"autoRepair"`
	}
	// An empty body is the pre-recipe call shape and stays valid.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	project, err := projectTag(body.Project)
	if err != nil {
		writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
		return
	}

	if body.DryRun || body.Goal != "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("edit the prepared SDK workflow before launching; dry runs and template goal overrides are not supported")))
		return
	}
	// Preparing for review keeps the template's questions as the workflow's own
	// inputs, so "use these steps" then Save yields something reusable rather
	// than this week's answers written into the prose. Launching fills them.
	res, err := s.store.InstantiateTemplate(r.Context(), id, body.Slots, project, body.Run)
	var missing *store.MissingSlotsError
	var loosened *store.LooseningError
	switch {
	case errors.As(err, &missing):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":        missing.Error(),
			"missingSlots": missing.Missing,
		})
		return
	// A run may tighten a tolerance the project stands behind and may never
	// widen one. That is the person's answer to correct, not a server fault, so
	// it comes back as a 400 naming each widened number rather than as a 500.
	case errors.As(err, &loosened):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":              loosened.Error(),
			"loosenedTolerances": loosened.Loosened,
		})
		return
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	case errors.Is(err, store.ErrInvalidWorkflow):
		writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
		return
	case err != nil:
		writeJSON(w, workflowErrorStatus(err, http.StatusInternalServerError), errBody(err))
		return
	}

	res.Workflow.AutoRepair = body.AutoRepair
	out := map[string]any{
		"workflow": res.Workflow,
		"recipe":   res.Template.ID,
		"slots":    res.Values,
		"criteria": res.Template.Criteria,
		"project":  project,
	}
	if body.Run {
		runID, err := s.runner.StartNodeWorkflow(res.Workflow.Instruction, res.Workflow.Nodes, project, body.AutoRepair,
			workflowSource{name: res.Template.Name, templateID: res.Template.ID, slots: res.Values})
		if err != nil {
			writeJSON(w, workflowErrorStatus(err, http.StatusInternalServerError), errBody(err))
			return
		}
		out["runId"] = runID
	}
	writeJSON(w, http.StatusCreated, out)
}
