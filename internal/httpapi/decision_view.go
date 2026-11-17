package httpapi

import (
	"context"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A decision ledger that renders `status` reads every verdict as "accepted".
// `status` is the ADR lifecycle of the RECORD — "this decision stands" — and
// the verdict a person gave is `decision`. Sofia's S-13 read a rejection filed
// as "accepted · journey:j-871c54fc4402e66b", directly above her own rejection
// note, on both the project page and the Inbox's Decided tab.
//
// The verdict itself was already on the wire. What was missing was anything
// beside it a person recognises: an outcome's `source` is `run:<id>` and a
// delivery's is `journey:<key>`, neither of which names the work. So the
// listing resolves the run each decision closed and names it the way every
// other surface does — the workflow's name, and which run of it — from the
// frozen launch record and the run ordinals. Nothing is stored.

// decisionView is a decision row plus the run it closed, named.
type decisionView struct {
	store.Decision
	// RunID is the run this decision closed: a run-grain outcome's own run, or
	// a delivery's latest attempt. Empty when the decision closed no run, which
	// is the honest answer for a hand-written architecture decision.
	RunID        string `json:"runId,omitempty"`
	WorkflowName string `json:"workflowName,omitempty"`
	RunNumber    int    `json:"runNumber,omitempty"`
}

// decisionRunID names the run a decision closed.
//
// A run-grain outcome says so in `source`. A delivery outcome's source is its
// journey, and its refs list every attempt with the LATEST first (see
// handleRecordJourneyOutcome) — which is the attempt the verdict was recorded
// against, so the first `run:` ref is the right one.
func decisionRunID(d store.Decision) string {
	if id, ok := strings.CutPrefix(d.Source, "run:"); ok && safeID(id) {
		return id
	}
	for _, ref := range d.Refs {
		if id, ok := strings.CutPrefix(ref, "run:"); ok && safeID(id) {
			return id
		}
	}
	return ""
}

// describeDecisions names the run behind each decision. It costs one read per
// distinct run plus one ordinals query, and a run that cannot be read leaves
// the row exactly as it was rather than failing the listing.
func (s *Server) describeDecisions(ctx context.Context, items []store.Decision) []decisionView {
	runs := map[string]store.Run{}
	ids := []string{}
	for _, d := range items {
		runID := decisionRunID(d)
		if runID == "" {
			continue
		}
		if _, seen := runs[runID]; seen {
			continue
		}
		run, err := s.store.GetRun(ctx, runID)
		if err != nil {
			continue
		}
		runs[runID] = run
		ids = append(ids, runID)
	}
	ordinals, _ := s.store.RunOrdinals(ctx, ids)

	out := make([]decisionView, 0, len(items))
	for _, d := range items {
		view := decisionView{Decision: d}
		if run, ok := runs[decisionRunID(d)]; ok {
			projected := projectRun(run, "", ordinals[run.RunID])
			view.RunID, view.WorkflowName, view.RunNumber = run.RunID, projected.WorkflowName, projected.RunNumber
		}
		out = append(out, view)
	}
	return out
}
