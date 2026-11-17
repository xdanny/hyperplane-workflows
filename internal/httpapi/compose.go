package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/compose"
	"github.com/xdanny/hyperplane-workflows/internal/sdkplanning"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleListBriefs returns the built-in starter compose briefs.
// GET /api/compose/briefs
func (s *Server) handleListBriefs(w http.ResponseWriter, _ *http.Request) {
	briefs, err := compose.StarterBriefs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, briefs)
}

// handleCompose proposes reviewed SDK steps. It never launches work,
// creates a graph draft, or silently saves/replaces an existing workflow.
func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Brief   string `json:"brief"`
		Project string `json:"project"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Brief) == "" || len(body.Brief) > 16000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "brief is required"})
		return
	}
	// A brief that still carries "[replace with …]" is refused here, before the
	// project is registered and before a single billed second of planning: a
	// starter brief nobody finished is not an intent (Q-25). The client refuses
	// it too; this is the half that cannot be bypassed.
	if msg := compose.PlaceholderRefusal(body.Brief); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":        msg,
			"placeholders": compose.CountPlaceholders(body.Brief),
		})
		return
	}
	project, err := projectTag(body.Project)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if err := s.store.EnsureProject(r.Context(), project); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	plan, err := sdkplanning.Propose(r.Context(), filepath.Join(s.cfg.RunsDir, "_compose"), body.Brief)
	if err != nil {
		writeJSON(w, workflowErrorStatus(err, http.StatusBadGateway), errBody(err))
		return
	}
	var workflow *store.SDKWorkflow
	if len(plan.Nodes) > 0 {
		instruction := body.Brief
		if plan.Instruction != "" {
			instruction = plan.Instruction
		}
		workflow = &store.SDKWorkflow{Name: plan.Name, Kind: "nodes", Instruction: instruction, Nodes: plan.Nodes, Check: []string{}, Project: project}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": workflow, "unsupported": plan.Unsupported, "project": project})
}
