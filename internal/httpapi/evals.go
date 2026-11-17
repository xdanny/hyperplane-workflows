package httpapi

import (
	"net/http"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func (s *Server) handleListEvalsets(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListEvalsets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if list == nil {
		list = []store.Evalset{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetEvalset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	es, err := s.store.GetEvalset(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, es)
}

func (s *Server) handleListEvalRuns(w http.ResponseWriter, r *http.Request) {
	evalsetID := r.PathValue("id")
	runs, err := s.store.ListEvalRuns(r.Context(), evalsetID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if runs == nil {
		runs = []store.EvalRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleGetEvalRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetEvalRun(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, run)
}
