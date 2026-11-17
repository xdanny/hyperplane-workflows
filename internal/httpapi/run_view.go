package httpapi

import (
	"encoding/json"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A run list used to read as the goal sentence repeated once per attempt, so
// every run of one workflow looked identical except for a relative time and an
// id tail. These derived fields are what a person needs to tell them apart:
// which workflow, which run of it, how long it took, which values it was given,
// which STEP broke, and whether it is actually waiting for them.
//
// Nothing here is stored. Every field is projected from the frozen launch
// record and the committed SDK checkpoint the run already carries.

// runFailingStep names the declared step that explains a bad ending. It is a
// step of the workflow a person wrote — never the internal "delivery" node,
// which names nothing they chose.
type runFailingStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// runView is a run row plus those derived fields. store.Run is embedded, so the
// wire shape stays a flat run object and existing consumers do not change.
type runView struct {
	store.Run
	// FailingNode names the node that failed or blocked (see
	// store.FailingNodesByRun). Kept unchanged for records that predate SDK
	// steps, where it is still the only answer available.
	FailingNode string `json:"failingNode,omitempty"`

	WorkflowName string            `json:"workflowName,omitempty"`
	RunNumber    int               `json:"runNumber,omitempty"`
	DurationMs   *int64            `json:"durationMs"`
	Slots        map[string]string `json:"slots"`
	// SlotLabels names those values the way the person was asked for them. It is
	// frozen at launch, so renaming an input later never relabels an old run.
	SlotLabels map[string]string `json:"slotLabels,omitempty"`
	// SignOff is the sign-off policy this run was launched under.
	SignOff          string          `json:"signOff,omitempty"`
	FailingStep      *runFailingStep `json:"failingStep"`
	WaitingForPerson bool            `json:"waitingForPerson"`
}

// sdkLaunch is the part of a launch record these fields are projected from.
type sdkLaunch struct {
	Runtime string `json:"runtime"`
	Request struct {
		Workflow   string            `json:"workflow"`
		Slots      map[string]string `json:"slots"`
		SlotLabels map[string]string `json:"slotLabels"`
		Nodes      []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Kind  string `json:"kind"`
		} `json:"nodes"`
	} `json:"request"`
	ProjectWorkflow struct {
		Name string `json:"name"`
	} `json:"projectWorkflow"`
}

// sdkCheckpoint is the part of a committed checkpoint these fields read.
type sdkCheckpoint struct {
	SDK struct {
		Phase string `json:"phase"`
		Nodes []struct {
			ID       string `json:"id"`
			Phase    string `json:"phase"`
			Decision string `json:"decision"`
		} `json:"nodes"`
	} `json:"sdk"`
}

// projectRun derives the display fields for one run. A run that is not a
// codex-sdk launch keeps exactly the shape it had before.
func projectRun(run store.Run, failingNode string, runNumber int) runView {
	view := runView{Run: run, FailingNode: failingNode, RunNumber: runNumber}
	var launch sdkLaunch
	if len(run.Options) == 0 || json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" {
		return view
	}
	view.WorkflowName = launch.ProjectWorkflow.Name
	view.Slots, view.SlotLabels = launch.Request.Slots, launch.Request.SlotLabels
	view.SignOff = store.RunSignOff(run)
	if !run.StartedAt.IsZero() && !run.EndedAt.IsZero() {
		ms := run.EndedAt.Sub(run.StartedAt).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		view.DurationMs = &ms
	}
	var checkpoint sdkCheckpoint
	if len(run.Doc) == 0 || json.Unmarshal(run.Doc, &checkpoint) != nil {
		return view
	}
	// The store keeps such a run "running": only the presentation changes, so a
	// person can see the difference between work in progress and their own turn.
	view.WaitingForPerson = checkpoint.SDK.Phase == "approval_pending"
	states := map[string]string{}
	for _, node := range checkpoint.SDK.Nodes {
		states[node.ID] = node.Phase
	}
	for _, node := range launch.Request.Nodes {
		if phase := states[node.ID]; phase == "failed" || phase == "rejected" {
			kind := node.Kind
			if kind == "" {
				kind = "document"
			}
			view.FailingStep = &runFailingStep{ID: node.ID, Title: node.Title, Kind: kind}
			break
		}
	}
	return view
}

// projectRuns decorates a page of runs. It costs one extra query for the run
// numbers and one for the failing nodes, whatever the page size.
func (s *Server) projectRuns(runs []store.Run, failing map[string]string, ordinals map[string]int) []runView {
	out := make([]runView, 0, len(runs))
	for _, run := range runs {
		out = append(out, projectRun(run, failing[run.RunID], ordinals[run.RunID]))
	}
	return out
}
