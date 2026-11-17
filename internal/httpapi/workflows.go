package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

// Validation is read-only and uses the same SDK rules as save and execution.
func (s *Server) handleValidateSDKWorkflow(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Instruction string             `json:"instruction"`
		Nodes       []sdkworkflow.Node `json:"nodes"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("provide one workflow definition")))
		return
	}
	report, err := sdkworkflow.Inspect(r.Context(), request.Instruction, request.Nodes)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func workflowErrorStatus(err error, fallback int) int {
	if errors.Is(err, sdkworkflow.ErrValidationUnavailable) {
		return http.StatusServiceUnavailable
	}
	return fallback
}

func (s *Server) handleWorkflowChecks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"checks": agentchecks.List()})
}

func (s *Server) handleListSDKWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSDKWorkflows(r.Context())
	if err != nil {
		writeJSON(w, 500, errBody(err))
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) handleSaveSDKWorkflow(w http.ResponseWriter, r *http.Request) {
	var item store.SDKWorkflow
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil {
		writeJSON(w, 400, errBody(err))
		return
	}
	if item.ID != "" && item.ID != r.PathValue("id") {
		writeJSON(w, 400, errBody(store.ErrInvalidWorkflow))
		return
	}
	item.ID = r.PathValue("id")
	item, err := s.store.SaveSDKWorkflow(r.Context(), item)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == "POST" {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}

func (s *Server) handleDeleteSDKWorkflow(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeJSON(w, 400, errBody(store.ErrInvalidWorkflow))
		return
	}
	if err := s.store.DeleteSDKWorkflow(r.Context(), r.PathValue("id"), revision); err != nil {
		writeWorkflowError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"removed": true})
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	// A token naming no declared input is a specific, fixable mistake: name the
	// ids so the editor can point at each one instead of printing a sentence.
	var unknown *store.UnknownInputsError
	if errors.As(err, &unknown) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":         unknown.Error(),
			"unknownInputs": unknown.Unknown,
		})
		return
	}
	status := http.StatusInternalServerError
	if errors.Is(err, store.ErrInvalidWorkflow) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, store.ErrWorkflowChanged) {
		status = http.StatusConflict
	}
	writeJSON(w, workflowErrorStatus(err, status), errBody(err))
}
