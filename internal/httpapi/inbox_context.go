package httpapi

import (
	"context"
	"encoding/json"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// An inbox card that names nothing but a hash asks a person to decide something
// they cannot see. These fields put the question in context — which workflow,
// which run of it, what it was trying to do, what happens next, and the actual
// document to read — from records the run already carries.

// inboxLink is one document a card points at.
type inboxLink struct {
	Title string `json:"title,omitempty"`
	Path  string `json:"path,omitempty"`
	URL   string `json:"url"`
}

// inboxApproval is a pending approval plus the context its card needs.
type inboxApproval struct {
	store.Record
	WorkflowName  string     `json:"workflowName,omitempty"`
	RunNumber     int        `json:"runNumber,omitempty"`
	Goal          string     `json:"goal,omitempty"`
	NextSteps     []string   `json:"nextSteps"`
	DraftDocument *inboxLink `json:"draftDocument"`
}

// inboxOutcome is a delivery awaiting a verdict plus the same context.
type inboxOutcome struct {
	journey.Journey
	WorkflowName   string     `json:"workflowName,omitempty"`
	RunNumber      int        `json:"runNumber,omitempty"`
	ResultDocument *inboxLink `json:"resultDocument"`
	FailureReason  string     `json:"failureReason,omitempty"`
}

// describeApprovals loads each pending approval's run once and derives the
// card context. A run that cannot be read still yields the plain record.
func (s *Server) describeApprovals(ctx context.Context, records []store.Record) []inboxApproval {
	runs := map[string]store.Run{}
	ids := []string{}
	for _, record := range records {
		if record.RunID == "" {
			continue
		}
		if _, seen := runs[record.RunID]; seen {
			continue
		}
		run, err := s.store.GetRun(ctx, record.RunID)
		if err != nil {
			continue
		}
		runs[record.RunID] = run
		ids = append(ids, record.RunID)
	}
	ordinals, _ := s.store.RunOrdinals(ctx, ids)

	out := make([]inboxApproval, 0, len(records))
	for _, record := range records {
		item := inboxApproval{Record: record, NextSteps: []string{}}
		var doc struct {
			GateNode  string              `json:"gateNode"`
			Documents []map[string]string `json:"documents"`
		}
		if len(record.Doc) > 0 {
			_ = json.Unmarshal(record.Doc, &doc)
		}
		if len(doc.Documents) > 0 {
			first := doc.Documents[0]
			item.DraftDocument = &inboxLink{Title: first["name"], Path: first["file"], URL: first["url"]}
		}
		if run, ok := runs[record.RunID]; ok {
			view := projectRun(run, "", ordinals[record.RunID])
			item.WorkflowName, item.RunNumber, item.Goal = view.WorkflowName, view.RunNumber, run.Goal
			item.NextSteps = sdkDependentTitles(run, doc.GateNode)
		}
		out = append(out, item)
	}
	return out
}

// describeOutcomes adds the same context to the deliveries awaiting a verdict,
// reading the newest attempt of each.
func (s *Server) describeOutcomes(ctx context.Context, journeys []journey.Journey) []inboxOutcome {
	runs := map[string]store.Run{}
	latest := map[string]string{}
	ids := []string{}
	for _, item := range journeys {
		runID := item.Root
		for _, attempt := range item.Attempts {
			if !attempt.Superseded {
				runID = attempt.RunID
			}
		}
		latest[item.Key] = runID
		if runID == "" {
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

	out := make([]inboxOutcome, 0, len(journeys))
	for _, item := range journeys {
		enriched := inboxOutcome{Journey: item}
		run, ok := runs[latest[item.Key]]
		if ok {
			view := projectRun(run, "", ordinals[run.RunID])
			enriched.WorkflowName, enriched.RunNumber = view.WorkflowName, view.RunNumber
			if documents := sdkFinalDocuments(run); len(documents) > 0 {
				last := documents[len(documents)-1]
				enriched.ResultDocument = &inboxLink{Title: last.Title, Path: last.Path,
					URL: "/file/" + run.RunID + "/nodes/delivery/OUTPUTS/" + last.Path}
			}
			// The whole finding, not a card-sized slice of it: a person deciding
			// what a failure was worth needs the sentence that explains it.
			enriched.FailureReason = journey.InspectRun(run).FailureReason
		}
		out = append(out, enriched)
	}
	return out
}

// sdkDependentTitles names the steps that WAIT on one step. An approval with no
// dependents changes nothing downstream, and the empty list says so.
func sdkDependentTitles(run store.Run, stepID string) []string {
	out := []string{}
	if stepID == "" {
		return out
	}
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Nodes []struct {
				ID        string          `json:"id"`
				Title     string          `json:"title"`
				DependsOn json.RawMessage `json:"dependsOn"`
			} `json:"nodes"`
		} `json:"request"`
	}
	if len(run.Options) == 0 || json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" {
		return out
	}
	nodes := launch.Request.Nodes
	for i, node := range nodes {
		var explicit []string
		if len(node.DependsOn) > 0 && json.Unmarshal(node.DependsOn, &explicit) == nil && explicit != nil {
			for _, parent := range explicit {
				if parent == stepID {
					out = append(out, node.Title)
					break
				}
			}
			continue
		}
		if i > 0 && nodes[i-1].ID == stepID {
			out = append(out, node.Title)
		}
	}
	return out
}
