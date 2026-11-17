package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// launchSavedWorkflow runs a saved definition again with this run's values.
//
// The client sends an id and values, never steps. The server reads the saved
// workflow, substitutes the declared inputs with the same FillSlots templates
// use, and launches the result through the ordinary SDK path — so the SDK
// validator still admits the run and the launch still freezes what it reviewed.
//
// A supplied revision is a concurrency check, not a permission: it says "run
// the version I was looking at", and a 409 names the current one rather than
// silently running a definition the person has not seen.
func (s *Server) launchSavedWorkflow(w http.ResponseWriter, r *http.Request, id string, revision *int64, values map[string]string, project string, autoRepair *bool) {
	saved, err := s.store.GetSDKWorkflow(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("saved workflow %q is unavailable; reload the workflow list", id)))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if revision != nil && *revision != saved.Revision {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    store.ErrWorkflowChanged.Error(),
			"revision": saved.Revision,
		})
		return
	}
	filled, accepted, err := store.FillWorkflowSlots(saved, values)
	var missing *store.MissingSlotsError
	if errors.As(err, &missing) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":        missing.Error(),
			"missingSlots": missing.Missing,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if project == "" {
		project = saved.Project
	}
	repair := saved.AutoRepair
	if autoRepair != nil {
		repair = *autoRepair
	}
	source := workflowSource{id: saved.ID, name: saved.Name, slots: accepted,
		slotLabels: store.SlotLabels(saved.Inputs, accepted), signOff: saved.SignOff, revision: saved.Revision}

	var runID string
	switch filled.Kind {
	case "nodes":
		runID, err = s.runner.StartNodeWorkflow(filled.Instruction, filled.Nodes, project, repair, source)
	case "task":
		runID, err = s.runner.StartCheckedTask(filled.Instruction, filled.Check, filled.TargetRoot, project, repair, source)
	case "app-ideator":
		runID, err = s.runner.StartAppIdeator(map[string]string{"domain": filled.Instruction}, filled.Instruction, project, repair, source)
	default:
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("saved workflow %q has an unsupported kind %q", id, filled.Kind)))
		return
	}
	if err != nil {
		writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"runId": runID})
}
