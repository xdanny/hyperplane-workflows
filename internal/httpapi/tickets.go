package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"github.com/xdanny/hyperplane-workflows/internal/tickets"
)

// ── Lanes ────────────────────────────────────────────────────────────────────

// handleListLanes returns lanes with ticket status counts, optionally scoped
// to a project. ?project= absent returns all lanes; ?project= (empty value)
// returns only unscoped lanes; ?project=x returns only lanes scoped to x.
// GET /api/lanes[?project=hyperplane]
func (s *Server) handleListLanes(w http.ResponseWriter, r *http.Request) {
	lanes, err := s.store.ListLanes(r.Context(), projectFilter(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if lanes == nil {
		lanes = []*store.LaneWithCounts{}
	}
	writeJSON(w, http.StatusOK, lanes)
}

// handleCreateLane creates a new swim-lane.
// POST /api/lanes  body: {name, description?, targetRoot?, project?}
func (s *Server) handleCreateLane(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		TargetRoot  string `json:"targetRoot"`
		Project     string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'name' is required")))
		return
	}
	l := &store.Lane{
		ID:          time.Now().Format("20060102-150405.000") + "-lane",
		Name:        body.Name,
		Description: body.Description,
		Status:      "active",
		TargetRoot:  body.TargetRoot,
		Project:     body.Project,
	}
	if err := s.store.CreateLane(r.Context(), l); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

// handleGetLaneTickets returns all tickets in a lane.
// GET /api/lanes/{id}/tickets
func (s *Server) handleGetLaneTickets(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tix, err := s.store.ListTicketsByLane(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if tix == nil {
		tix = []*store.Ticket{}
	}
	writeJSON(w, http.StatusOK, tix)
}

// handleStartLaneWork admits one ready task into the managed SDK lifecycle.
// POST /api/lanes/{id}/work  body: {count}
func (s *Server) handleStartLaneWork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count      int    `json:"count"`
		Engine     string `json:"engine"`
		Review     bool   `json:"review"`
		MaxRetries int    `json:"maxRetries"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, 400, errBody(err))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, 400, errBody(fmt.Errorf("expected one request object")))
		return
	}
	if (body.Count != 0 && body.Count != 1) || body.Review || body.MaxRetries != 0 || (body.Engine != "" && body.Engine != "codex" && body.Engine != "codex-sdk") {
		writeJSON(w, 400, errBody(fmt.Errorf("Board execution runs one SDK task for human review; parallel workers, automatic review/retries and legacy engine overrides are unsupported")))
		return
	}
	source, err := s.store.NextReadySDKTicket(r.Context(), r.PathValue("id"))
	if err != nil {
		status := 500
		if errors.Is(err, store.ErrNotFound) {
			status = 404
		}
		if errors.Is(err, store.ErrTicketNotReady) || errors.Is(err, sdkruntime.ErrWorkspaceBusy) {
			status = 409
		}
		writeJSON(w, status, errBody(err))
		return
	}
	target := source.Lane.TargetRoot
	if target == "" {
		target = s.cfg.TargetRoot
	}
	id, err := s.runner.StartLaneTicket(source, target, s.cfg.PlatformRoot)
	if err != nil {
		status := workflowErrorStatus(err, 400)
		if errors.Is(err, store.ErrTicketNotReady) || errors.Is(err, sdkruntime.ErrWorkspaceBusy) {
			status = 409
		}
		writeJSON(w, status, errBody(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"laneId": source.Lane.ID, "ticketId": source.Ticket.ID, "runId": id})
}

// ── Tickets ──────────────────────────────────────────────────────────────────

// handleCreateTicket creates a new ticket.
// POST /api/tickets  body: {laneId, title, detail?, priority?, deps?}
func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LaneID   string   `json:"laneId"`
		Title    string   `json:"title"`
		Detail   string   `json:"detail"`
		Priority int      `json:"priority"`
		Deps     []string `json:"deps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if body.LaneID == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'laneId' is required")))
		return
	}
	if body.Title == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'title' is required")))
		return
	}
	if body.Priority == 0 {
		body.Priority = 2
	}
	if body.Priority < 1 || body.Priority > 3 {
		writeJSON(w, 400, errBody(fmt.Errorf("priority must be 1, 2 or 3")))
		return
	}
	if body.Deps == nil {
		body.Deps = []string{}
	}
	t := &store.Ticket{
		ID:       time.Now().Format("20060102-150405.000") + "-ticket",
		LaneID:   body.LaneID,
		Title:    body.Title,
		Detail:   body.Detail,
		Status:   "todo",
		Priority: body.Priority,
		Deps:     body.Deps,
	}
	if err := s.store.CreateTicket(r.Context(), t); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// handleGetTicket returns a ticket by ID.
// GET /api/tickets/{id}
func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.store.GetTicket(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handlePatchTicket updates mutable ticket fields.
// PATCH /api/tickets/{id}  body: {status?, assignee?, output?}
func (s *Server) handlePatchTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Fetch current state so we can fill unset fields.
	cur, err := s.store.GetTicket(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	var body struct {
		Status            *string    `json:"status"`
		Assignee          *string    `json:"assignee"`
		Output            *string    `json:"output"`
		ExpectedUpdatedAt *time.Time `json:"expectedUpdatedAt"`
		ExpectedRunID     *string    `json:"expectedRunId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}

	if cur.SDKRunID != "" && (body.ExpectedUpdatedAt == nil || body.ExpectedRunID == nil) {
		writeJSON(w, http.StatusConflict, errBody(fmt.Errorf("refresh the task and provide expectedUpdatedAt and expectedRunId before editing a managed task")))
		return
	}
	expectedUpdatedAt := cur.UpdatedAt
	if body.ExpectedUpdatedAt != nil {
		expectedUpdatedAt = *body.ExpectedUpdatedAt
	}
	if body.ExpectedRunID != nil && *body.ExpectedRunID != cur.SDKRunID {
		writeJSON(w, http.StatusConflict, errBody(store.ErrTicketChanged))
		return
	}

	newStatus := cur.Status
	newAssignee := cur.Assignee
	newOutput := cur.Output
	if body.Status != nil {
		newStatus = *body.Status
	}
	if body.Assignee != nil {
		newAssignee = *body.Assignee
	}
	if body.Output != nil {
		newOutput = *body.Output
	}

	if err := s.store.UpdateTicketStatus(r.Context(), id, newStatus, newAssignee, newOutput, cur.Error, expectedUpdatedAt); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrRunActive) || errors.Is(err, store.ErrTicketChanged) {
			status = http.StatusConflict
		}
		writeJSON(w, status, errBody(err))
		return
	}

	updated, err := s.store.GetTicket(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ── Decompose ─────────────────────────────────────────────────────────────────

// handleDecomposeLane handles POST /api/lanes/decompose.
//
// Request body (JSON):
//
//	{
//	  "fromRun":           "<completed SDK run id>",  // mode A: decompose from a run
//	  "name":              "<app name>",         // mode B: explicit config
//	  "concept":           "<one-liner>",
//	  "features":          ["f1","f2"],
//	  "nativeIntegrations":["n1"],
//	  "targetRoot":        "<path>",
//	  "engine":            "codex",              // optional; default "codex"
//	  "project":           "<project>"           // optional; mode B only — mode A copies the run's project
//	}
//
// Response (201): {"laneId": "...", "ticketIds": ["..."]}
func (s *Server) handleDecomposeLane(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromRun            string   `json:"fromRun"`
		Name               string   `json:"name"`
		Concept            string   `json:"concept"`
		Features           []string `json:"features"`
		NativeIntegrations []string `json:"nativeIntegrations"`
		TargetRoot         string   `json:"targetRoot"`
		Engine             string   `json:"engine"`
		Project            string   `json:"project"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("expected one request object")))
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.FromRun = strings.TrimSpace(body.FromRun)
	if (body.FromRun == "") == (body.Name == "") {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("supply exactly one of 'fromRun' or 'name'")))
		return
	}
	if body.FromRun != "" && (body.Concept != "" || len(body.Features) > 0 || len(body.NativeIntegrations) > 0 || body.Project != "") {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("fromRun supplies the source context and project; do not combine it with explicit planning fields")))
		return
	}
	engine := body.Engine
	if engine == "" {
		engine = "codex"
	}
	if engine != "codex" && engine != "codex-local" && engine != "codex-oss" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("unsupported engine")))
		return
	}
	project, err := projectTag(body.Project)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	cfg := tickets.DecomposeConfig{AppName: body.Name, Concept: body.Concept, Features: body.Features, NativeIntegrations: body.NativeIntegrations, TargetRoot: body.TargetRoot, Project: project}
	if body.FromRun != "" {
		cfg, err = tickets.ConfigFromRun(r.Context(), s.store, body.FromRun, body.TargetRoot)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, tickets.ErrInvalidSource) {
				status = http.StatusBadRequest
			}
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, errBody(err))
			return
		}
	}
	// Planning is request-owned; no lane is visible until its full ticket set commits.
	laneID, ticketIDs, err := tickets.DecomposeToTickets(r.Context(), s.store, cfg, tickets.NewCodexPlanner(engine))
	if err != nil {
		writeJSON(w, workflowErrorStatus(err, http.StatusBadGateway), errBody(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"laneId": laneID, "ticketIds": ticketIDs})
}
