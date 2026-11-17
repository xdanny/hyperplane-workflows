package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleListProjects returns every project — registered rows and any project
// tag still in use without one — with a combined item count, the per-kind
// breakdown, and last activity.
//
// GET /api/projects[?includeTest=true]
//
// includeTest keeps provenance='test' rows, which only appear on a database
// shared with the data-engineering line's fixtures.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	includeTest, err := boolParam(r, "includeTest")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	projects, err := s.store.ListProjects(r.Context(), includeTest)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if projects == nil {
		projects = []store.ProjectCount{}
	}
	writeJSON(w, http.StatusOK, projects)
}

// handleGetProject returns one project's summary row plus the workflows it has
// actually run (derived from its runs, never declared).
// GET /api/projects/{id}
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid project id")))
		return
	}
	detail, err := s.store.GetProjectDetail(r.Context(), id)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleCreateProject registers a project. Insert-only: an id that already
// exists is 409, so creating never overwrites an existing description.
// POST /api/projects  body: {id, name?, summary?, declarations?, status?, owner?}
//
// `summary` is the human's "what this project is for". `narrative` is not
// accepted here at any price: "what this project has built so far" is written
// by internal/brief from accepted deliveries, and a brand-new project has none.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body store.Project
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	// An id may be derived from the name, but never invented: slugifyOr with an
	// empty fallback means "no usable id" stays empty and is rejected below.
	if body.ID == "" {
		body.ID = slugifyOr(body.Name, "")
	}
	if !safeID(body.ID) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'id' is required and must not contain path characters")))
		return
	}
	body.Narrative = ""
	out, err := s.store.CreateProject(r.Context(), body)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// handlePatchProject edits a project. Every field is optional — an absent
// field is left alone, which is what lets the UI change a description without
// resending the name.
// PATCH /api/projects/{id}  body: {name?, summary?, declarations?, status?, owner?}
//
// `declarations` is merged group by group, so a client may send only the groups
// it knows about (see store.MergeDeclarations). `narrative` is machine-owned
// and silently dropped here — it is regenerated through
// POST /api/projects/{id}/narrative, never typed.
func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid project id")))
		return
	}
	var patch store.ProjectPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	patch.Narrative = nil
	out, err := s.store.UpdateProject(r.Context(), id, patch)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// projectTag normalizes a `project` field arriving on a request body that
// SCOPES something (a composition, a recipe instantiation) rather than
// filtering by one.
//
// Empty is legal and means unscoped — a user who never names a project must
// still be able to compose. Anything else has to be a safe id, because the tag
// becomes a path segment on /api/projects/{id} the moment the project is
// auto-registered; catching it here turns a would-be 404 later into a clear
// 400 now.
func projectTag(raw string) (string, error) {
	tag := strings.TrimSpace(raw)
	if tag == "" {
		return "", nil
	}
	if !safeID(tag) {
		return "", fmt.Errorf("invalid project %q: must not contain path characters", raw)
	}
	return tag, nil
}

// writeProjectError maps the store's sentinels onto status codes. Anything
// else is a real server-side failure and says so rather than being flattened
// into a 400.
func writeProjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(err))
	case errors.Is(err, store.ErrExists):
		writeJSON(w, http.StatusConflict, errBody(err))
	// A declaration that is not one is the caller's document, not a server
	// fault: 400 with the sentences, so the form can print them beside the row.
	case errors.Is(err, store.ErrInvalidDeclarations):
		writeJSON(w, http.StatusBadRequest, errBody(err))
	default:
		writeJSON(w, http.StatusInternalServerError, errBody(err))
	}
}

// boolParam reads an optional boolean query param. Absent is false; a value
// that is not a boolean is an error rather than a silent false.
func boolParam(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %q is not a boolean", name, raw)
	}
	return v, nil
}
