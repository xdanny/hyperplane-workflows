package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A saved workflow had no history: nothing listed its runs, the values each one
// was given, what it produced or who accepted it. This read model answers that
// from records the runs already carry — the frozen launch request, the
// committed SDK checkpoint, the existing usage and decision read models. There
// is no new table and no history is rewritten.

// historyDocument is one produced file a run recorded for a final step.
//
// Kind is the declared step kind, because a terminal `check` step records a
// document too — its review — and a compare view that diffs the LAST document
// diffed "# Check the note" instead of the note (S-16). The deliverable is the
// last document of a step that is neither a review nor an approval, and only
// the kind makes that answerable on the client.
type historyDocument struct {
	StepID string `json:"stepId"`
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// historyOutcome is the person's recorded verdict on a run's delivery.
type historyOutcome struct {
	Verdict   string     `json:"verdict"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
	Note      string     `json:"note,omitempty"`
}

// historyUsage is the run's committed token usage, or null when none is recorded.
type historyUsage struct {
	InputTokens       int `json:"inputTokens"`
	CachedInputTokens int `json:"cachedInputTokens"`
	OutputTokens      int `json:"outputTokens"`
}

// historyRun is one row of a saved workflow's history.
type historyRun struct {
	RunID            string     `json:"runId"`
	RunNumber        int        `json:"runNumber"`
	StartedAt        time.Time  `json:"startedAt"`
	EndedAt          *time.Time `json:"endedAt"`
	DurationMs       *int64     `json:"durationMs"`
	State            string     `json:"state"`
	WaitingForPerson bool       `json:"waitingForPerson"`
	// SignOff is the policy THIS run was launched under, not the saved
	// workflow's policy today. A history table that reads one current policy
	// over every row restates old runs under terms they never ran with; a run
	// with no recorded policy is "required", which is what that run did.
	SignOff        string            `json:"signOff"`
	Slots          map[string]string `json:"slots"`
	SlotLabels     map[string]string `json:"slotLabels,omitempty"`
	ChecksDeclared int               `json:"checksDeclared"`
	ChecksPassed   int               `json:"checksPassed"`
	FailingStep    *runFailingStep   `json:"failingStep"`
	Outcome        *historyOutcome   `json:"outcome"`
	Usage          *historyUsage     `json:"usage"`
	Documents      []historyDocument `json:"documents"`
	// Files is EVERY checkpoint file of every step, documents included, so two
	// runs can be compared on what they produced and not only on their prose.
	Files []historyFile `json:"files"`
}

// historyFile is one file a step recorded. It names the step, because "which
// run changed totals.csv" is only answerable if the file says who wrote it.
type historyFile struct {
	StepID    string `json:"stepId"`
	StepTitle string `json:"stepTitle"`
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// attachableRun is one earlier run a person could attach to this workflow.
type attachableRun struct {
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
	State     string    `json:"state"`
	Reason    string    `json:"reason"`
}

// handleWorkflowRuns returns one saved workflow and the runs launched from it,
// newest first. Read-only: it starts nothing and decides nothing.
func (s *Server) handleWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	workflow, err := s.store.GetSDKWorkflow(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	runs, total, err := s.store.RunsForSavedWorkflow(r.Context(), id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.RunID)
	}
	ordinals, _ := s.store.RunOrdinals(r.Context(), ids)

	out := make([]historyRun, 0, len(runs))
	for _, run := range runs {
		view := projectRun(run, "", ordinals[run.RunID])
		row := historyRun{
			RunID: run.RunID, RunNumber: view.RunNumber, StartedAt: run.StartedAt,
			DurationMs: view.DurationMs, State: run.State, WaitingForPerson: view.WaitingForPerson,
			Slots: view.Slots, SlotLabels: view.SlotLabels, FailingStep: view.FailingStep,
			SignOff:   store.RunSignOff(run),
			Documents: sdkFinalDocuments(run), Files: sdkRecordedFiles(run),
		}
		if !run.EndedAt.IsZero() {
			ended := run.EndedAt
			row.EndedAt = &ended
		}
		for _, criterion := range journey.InspectRun(run).Criteria {
			row.ChecksDeclared++
			if criterion.Proven {
				row.ChecksPassed++
			}
		}
		if decision, derr := s.store.DeliveryDecisionForRun(r.Context(), run.RunID); derr == nil {
			row.Outcome = &historyOutcome{Verdict: decision.Decision, DecidedAt: decision.DecidedAt, Note: decision.Why}
		}
		if usage := sdkRunTokens(run); usage.InputTokens > 0 || usage.OutputTokens > 0 {
			row.Usage = &historyUsage{InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens}
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": workflow, "runs": out, "total": total,
		"attachable": s.attachableRuns(r.Context(), workflow)})
}

// attachableRuns offers the earlier runs that executed this exact definition
// without a saved source. Read-only and best-effort: the history page must
// still load when the offer cannot be computed.
func (s *Server) attachableRuns(ctx context.Context, workflow store.SDKWorkflow) []attachableRun {
	out := []attachableRun{}
	runs, err := s.store.AttachableRuns(ctx, workflow, 50)
	if err != nil {
		return out
	}
	for _, run := range runs {
		out = append(out, attachableRun{RunID: run.RunID, StartedAt: run.StartedAt, State: run.State, Reason: store.AttachReason})
	}
	return out
}

// handleAttachRuns regroups earlier runs under this saved workflow. It moves
// identity only: each run keeps the frozen request it actually executed, and a
// run that ran something else — or already has a saved source — is refused by
// name rather than quietly skipped.
func (s *Server) handleAttachRuns(w http.ResponseWriter, r *http.Request) {
	workflow, err := s.store.GetSDKWorkflow(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	var body struct {
		RunIDs []string `json:"runIds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if len(body.RunIDs) == 0 || len(body.RunIDs) > 200 {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("name between 1 and 200 runs to attach")))
		return
	}
	attached := []string{}
	skipped := []map[string]string{}
	for _, runID := range body.RunIDs {
		if !safeID(runID) {
			skipped = append(skipped, map[string]string{"runId": runID, "reason": "not a run id"})
			continue
		}
		err := s.store.AttachRunToWorkflow(r.Context(), workflow, runID)
		var refused *store.ErrNotAttachable
		switch {
		case err == nil:
			attached = append(attached, runID)
		case errors.Is(err, store.ErrNotFound):
			skipped = append(skipped, map[string]string{"runId": runID, "reason": "this run no longer exists"})
		case errors.As(err, &refused):
			skipped = append(skipped, map[string]string{"runId": runID, "reason": refused.Reason})
		default:
			writeJSON(w, http.StatusInternalServerError, errBody(err))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"attached": attached, "skipped": skipped})
}

// sdkRecordedFiles returns every file the committed checkpoint recorded for
// every step — the step's document and any other file it wrote. Comparing two
// runs on prose alone hid a changed data file; this is what makes the change
// visible. Paths are what the checkpoint recorded; nothing is read from disk.
func sdkRecordedFiles(run store.Run) []historyFile {
	titles := map[string]string{}
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Nodes []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"nodes"`
		} `json:"request"`
	}
	if len(run.Options) == 0 || json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" {
		return []historyFile{}
	}
	for _, node := range launch.Request.Nodes {
		titles[node.ID] = node.Title
	}
	type recordedFile struct {
		Path   string `json:"path"`
		Bytes  int64  `json:"bytes"`
		SHA256 string `json:"sha256"`
	}
	var doc struct {
		SDK struct {
			Nodes []struct {
				ID       string         `json:"id"`
				Document *recordedFile  `json:"document"`
				Files    []recordedFile `json:"files"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	out := []historyFile{}
	if len(run.Doc) == 0 || json.Unmarshal(run.Doc, &doc) != nil {
		return out
	}
	for _, node := range doc.SDK.Nodes {
		seen := map[string]bool{}
		add := func(file recordedFile) {
			if file.Path == "" || seen[file.Path] {
				return
			}
			seen[file.Path] = true
			out = append(out, historyFile{StepID: node.ID, StepTitle: titles[node.ID], Path: file.Path, Bytes: file.Bytes, SHA256: file.SHA256})
		}
		if node.Document != nil {
			add(*node.Document)
		}
		for _, file := range node.Files {
			add(file)
		}
	}
	return out
}

// sdkFinalDocuments returns the documents recorded for the steps NOTHING waits
// on — the workflow's results, rather than every intermediate note it wrote on
// the way. Dependencies come from the frozen request: an explicit list is
// authoritative, an omitted one means the step before it.
func sdkFinalDocuments(run store.Run) []historyDocument {
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Nodes []struct {
				ID        string          `json:"id"`
				Title     string          `json:"title"`
				Kind      string          `json:"kind"`
				DependsOn json.RawMessage `json:"dependsOn"`
			} `json:"nodes"`
		} `json:"request"`
	}
	if len(run.Options) == 0 || json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" {
		return []historyDocument{}
	}
	nodes := launch.Request.Nodes
	depended := map[string]bool{}
	for i, node := range nodes {
		var explicit []string
		if len(node.DependsOn) > 0 && json.Unmarshal(node.DependsOn, &explicit) == nil && explicit != nil {
			for _, parent := range explicit {
				depended[parent] = true
			}
			continue
		}
		if i > 0 {
			depended[nodes[i-1].ID] = true
		}
	}
	var doc struct {
		SDK struct {
			Nodes []struct {
				ID       string `json:"id"`
				Document *struct {
					Path   string `json:"path"`
					Bytes  int64  `json:"bytes"`
					SHA256 string `json:"sha256"`
				} `json:"document"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	files := map[string]historyDocument{}
	if len(run.Doc) > 0 && json.Unmarshal(run.Doc, &doc) == nil {
		for _, node := range doc.SDK.Nodes {
			if node.Document == nil || node.Document.Path == "" {
				continue
			}
			files[node.ID] = historyDocument{StepID: node.ID, Path: node.Document.Path, Bytes: node.Document.Bytes, SHA256: node.Document.SHA256}
		}
	}
	out := []historyDocument{}
	for _, node := range nodes {
		if depended[node.ID] {
			continue
		}
		if file, ok := files[node.ID]; ok {
			file.Title, file.Kind = node.Title, node.Kind
			out = append(out, file)
		}
	}
	return out
}
