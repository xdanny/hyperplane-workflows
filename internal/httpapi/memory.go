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

// handleListMemory returns all memory items, optionally filtered by ?kind=&limit=.
// GET /api/kb/memory
func (s *Server) handleListMemory(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.store.ListMemory(r.Context(), kind, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if items == nil {
		items = []store.MemoryItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleUpsertMemory creates or updates a memory item from the request body.
// POST /api/kb/memory
func (s *Server) handleUpsertMemory(w http.ResponseWriter, r *http.Request) {
	var item store.MemoryItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	if item.Key == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'key' is required")))
		return
	}
	if item.Kind == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("'kind' is required")))
		return
	}
	if err := s.store.UpsertMemory(r.Context(), item); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	// Return the stored item (fetch to pick up generated id + timestamps).
	stored, err := s.store.GetMemory(r.Context(), item.Key)
	if err != nil {
		writeJSON(w, http.StatusOK, item)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// handleSearchKB performs a unified search across skills, memory, decisions, and radar.
// GET /api/kb?q=<text>&kinds=skill,radar&limit=20
func (s *Server) handleSearchKB(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	var kinds []string
	if k := r.URL.Query().Get("kinds"); k != "" {
		for _, part := range strings.Split(k, ",") {
			if part = strings.TrimSpace(part); part != "" {
				kinds = append(kinds, part)
			}
		}
	}
	if r.URL.Query().Has("tags") {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("run-context tag search is retired; search skills, memory, decisions or radar")))
		return
	}
	for _, kind := range kinds {
		if kind != "skill" && kind != "memory" && kind != "decision" && kind != "radar" {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("unsupported knowledge kind %q", kind)))
			return
		}
	}
	results, err := s.store.SearchKB(r.Context(), store.KBQuery{
		Q:     q,
		Kinds: kinds,
		Limit: limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if results == nil {
		results = []store.KBResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

// handleDeleteMemory removes a memory item by key.
// DELETE /api/kb/memory/{key}
func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("key is required")))
		return
	}
	if err := s.store.DeleteMemory(r.Context(), key); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errBody(err))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": key})
}
