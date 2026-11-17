package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/contract"
	"github.com/xdanny/hyperplane-workflows/internal/events"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// handleListRuns returns runs filtered by optional ?state=, ?project= and
// ?limit= params. ?project= absent returns runs from all projects; ?project=
// (empty value) returns only unscoped runs; ?project=x scopes to x.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	f := store.RunFilter{
		State:   r.URL.Query().Get("state"),
		Project: projectFilter(r),
		// The list shows when each run STARTED, so it is ordered by that.
		ByStart: true,
	}
	if lim := r.URL.Query().Get("limit"); lim != "" {
		n, _ := strconv.Atoi(lim)
		f.Limit = n
	}
	runs, err := s.store.ListRuns(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}

	// Which node broke, for the runs that broke. One extra query for the page,
	// and only when the page actually contains an unhappy run.
	var unhappy []string
	for _, run := range runs {
		if run.State == "failed" || run.State == "blocked" || run.State == "interrupted" {
			unhappy = append(unhappy, run.RunID)
		}
	}
	// Best-effort: this decorates the list, so a failure here costs the inline
	// node id, never the list.
	failing, _ := s.store.FailingNodesByRun(r.Context(), unhappy)

	// Which run of which workflow this is. One query for the page; a failure
	// costs the run numbers, never the list.
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.RunID)
	}
	ordinals, _ := s.store.RunOrdinals(r.Context(), ids)

	writeJSON(w, http.StatusOK, s.projectRuns(runs, failing, ordinals))
}

// handleGetRun returns the SDK run, its delivery checkpoint and any recorded
// human outcome. The journey annotation groups attempts of the same delivery.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	run, err := s.store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	nodes, err := s.store.ListNodes(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if nodes == nil {
		nodes = []store.Node{}
	}
	// The frozen REQUEST block used to head itself "recipe: …33BAB14C" — an
	// identifier where a name belongs. Same resolver as the brief, the Inbox
	// and the projects panel; the uuid stays on the run doc, for the hover.
	if namer, nerr := s.store.NewRecipeNamer(r.Context()); nerr == nil {
		run.RecipeName = namer.Name(store.RunDocString(run, "recipe"))
	}
	ordinals, _ := s.store.RunOrdinals(r.Context(), []string{id})
	failing, _ := s.store.FailingNodesByRun(r.Context(), []string{id})
	writeJSON(w, http.StatusOK, map[string]any{
		"run":   projectRun(run, failing[id], ordinals[id]),
		"nodes": nodes,
		// outcome is this RUN's verdict, which remains recordable as an optional
		// annotation; journey is the delivery this run is a chapter of, whose
		// outcome is the one the inbox actually asks for.
		"outcome": s.lookupRunOutcome(r.Context(), id),
		"journey": s.journeyForRun(r.Context(), run),
	})
}

// handleCancelRun cancels an active (running) run by marking it interrupted.
// It also signals the in-process runner to stop launching new nodes.
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	if err := s.store.CancelRun(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errBody(err))
			return
		}
		if errors.Is(err, store.ErrNotRunning) {
			writeJSON(w, http.StatusConflict, errBody(err))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	// Best-effort: cancel the in-process context if this run is local.
	s.runner.Cancel(id)
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": id})
}

// handleDeleteRun removes a run and its nodes/events (FK cascade).
func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	if err := s.store.DeleteRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody(err))
		return
	} else if errors.Is(err, store.ErrRunActive) {
		writeJSON(w, http.StatusConflict, errBody(err))
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// handleListNodes returns all nodes for a run.
func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	nodes, err := s.store.ListNodes(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if nodes == nil {
		nodes = []store.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

// handleListEvents returns journal events for a run with optional ?since=&limit= pagination.
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		since, _ = strconv.ParseInt(s, 10, 64)
	}
	var limit int
	if l := r.URL.Query().Get("limit"); l != "" {
		limit, _ = strconv.Atoi(l)
	}
	evts, err := s.store.ListEvents(r.Context(), id, since, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if evts == nil {
		evts = []store.Event{}
	}
	writeJSON(w, http.StatusOK, evts)
}

// handleEventsStream streams live run events over SSE. It backfills from the
// journal first, then subscribes to the in-process Bus for live events.
func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}

	// Verify run exists.
	if _, err := s.store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Subscribe to live events from the bus.
	ch, unsub := s.bus.Subscribe(id)
	defer unsub()

	// Backfill after subscribing so events committed during the query cannot vanish.
	existing, _ := s.store.ListEvents(r.Context(), id, 0, 0)
	var lastSeq int64
	for _, e := range existing {
		writeSSEStoreEvent(w, e)
		lastSeq = e.Seq
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case e, open := <-ch:
			if !open {
				return
			}
			if e.Seq <= lastSeq {
				continue // already sent in backfill
			}
			writeSSEBusEvent(w, e)
			lastSeq = e.Seq
			flusher.Flush()
		}
	}
}

// writeSSEStoreEvent encodes a store.Event as an SSE data frame.
func writeSSEStoreEvent(w http.ResponseWriter, e store.Event) {
	b, _ := json.Marshal(e)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// writeSSEBusEvent encodes a live events.Event as an SSE data frame.
func writeSSEBusEvent(w http.ResponseWriter, e events.Event) {
	b, _ := json.Marshal(e)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// artifactEntry is returned by handleListArtifacts.
type artifactEntry struct {
	Media string `json:"media,omitempty"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	URL   string `json:"url"`
}

// maxArtifactDepth bounds how far handleListArtifacts descends into
// artifacts/ subdirectories: artifacts/a/b/c/file.txt (3 directory levels
// below artifacts/) is the deepest file listed. Anything nested further is
// skipped silently — a listing endpoint should degrade quietly, not 500,
// when a node writes an unexpectedly deep tree.
const maxArtifactDepth = 3

// handleListArtifacts lists files under <runDir>/artifacts, including files
// nested up to maxArtifactDepth directory levels deep (e.g.
// "audit/rules/foo.sql"). Names and URLs use slash-separated relative paths
// regardless of host OS.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid run id")))
		return
	}
	artDir, err := safeRunDir(s.cfg.RunsDir, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	runDir := artDir
	out, err := walkArtifacts(filepath.Join(runDir, "artifacts"), id)
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	if out == nil {
		out = []artifactEntry{}
	}
	// Node-owned outputs are the canonical artifacts for declared workflows.
	entries, err := os.ReadDir(filepath.Join(runDir, "nodes"))
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		files, err := walkArtifacts(filepath.Join(runDir, "nodes", entry.Name(), "OUTPUTS"), id)
		if err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, errBody(err))
			return
		}
		for _, file := range files {
			file.URL = "/file/" + id + "/nodes/" + entry.Name() + "/OUTPUTS/" + file.Name
			file.Name = entry.Name() + "/" + file.Name
			out = append(out, file)
		}
	}
	// Declared media is authoritative even when contract filenames have no suffix.
	if s.store != nil {
		nodes, err := s.store.ListNodes(r.Context(), id)
		if err == nil {
			media := map[string]string{}
			for _, node := range nodes {
				if env, ok := contract.DecodeEnvelope(node.Output); ok {
					for _, value := range env.Outputs {
						if value.Artifact != nil {
							media["/file/"+id+"/"+value.Artifact.Artifact] = value.Artifact.Media
						}
					}
				}
			}
			for i := range out {
				out[i].Media = media[out[i].URL]
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

// walkArtifacts lists regular files under artDir, at most maxArtifactDepth
// directory levels deep, and returns them sorted by (slash-separated,
// relative-to-artDir) Name.
//
// Symlinks are never followed: WalkDir already does not descend into a
// symlinked directory (fs.DirEntry reflects the entry's own Lstat-derived
// type, not the link target's), and a symlink to a file has a non-regular
// Type() here, so it is excluded by the d.Type().IsRegular() check below.
// Either shape — a symlinked dir or a symlinked file — could otherwise point
// outside the run directory, which this listing must never surface.
func walkArtifacts(artDir, runID string) ([]artifactEntry, error) {
	out := []artifactEntry{}
	err := filepath.WalkDir(artDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == artDir {
				return err // missing/unreadable root: let the caller classify it
			}
			return nil // per-entry error (e.g. permissions): skip, keep walking
		}
		if path == artDir {
			return nil
		}
		rel, err := filepath.Rel(artDir, path)
		if err != nil {
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if depth+1 > maxArtifactDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > maxArtifactDepth {
			return nil // too deep: the containing dir was allowed, this file is not
		}
		if !d.Type().IsRegular() {
			return nil // symlink, socket, device, etc. — never listed
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		out = append(out, artifactEntry{
			Name: relSlash,
			Size: info.Size(),
			URL:  "/file/" + runID + "/artifacts/" + relSlash,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// handlePostRun launches an explicit SDK workflow request.
func (s *Server) handlePostRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceWorkflowID string             `json:"sourceWorkflowId"`
		WorkflowName     string             `json:"workflowName"`
		Nodes            []sdkworkflow.Node `json:"nodes"`
		Task             string             `json:"task"`
		Check            []string           `json:"check"`
		Workflow         string             `json:"workflow"` // explicit SDK workflow name
		Inputs           map[string]string  `json:"inputs"`
		Goal             string             `json:"goal"`       // user-supplied label for this run
		Project          string             `json:"project"`    // project scope for this run
		TargetRoot       string             `json:"targetRoot"` // override server default target root
		AutoRepair       *bool              `json:"autoRepair"` // spend the one bounded repair automatically on failure
		// Slots and Revision launch a SAVED workflow with this run's values.
		// The server reads the saved steps; the client sends no definition.
		Slots    map[string]string `json:"slots"`
		Revision *int64            `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("invalid body: %w", err)))
		return
	}
	autoRepair := body.AutoRepair != nil && *body.AutoRepair

	// Launching a saved workflow sends its id and this run's values, never a
	// definition: the server reads the saved steps so the run is the saved
	// workflow, not a client's copy of it.
	definitionSubmitted := body.Nodes != nil || body.Task != "" || body.Check != nil || body.Workflow != "" || body.TargetRoot != "" || len(body.Inputs) != 0
	if !definitionSubmitted && body.SourceWorkflowID != "" {
		s.launchSavedWorkflow(w, r, body.SourceWorkflowID, body.Revision, body.Slots, body.Project, body.AutoRepair)
		return
	}
	if len(body.Slots) != 0 || body.Revision != nil {
		writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("inputs and a revision launch a saved workflow; send sourceWorkflowId on its own, without steps or a task")))
		return
	}

	if body.Nodes != nil {
		if body.Task != "" || body.Check != nil || body.TargetRoot != "" || body.Workflow != "" || len(body.Inputs) != 0 {
			writeJSON(w, 400, errBody(fmt.Errorf("node workflows use their own folder; provide a goal and steps without task or graph settings")))
			return
		}
		id, err := s.runner.StartNodeWorkflow(body.Goal, body.Nodes, body.Project, autoRepair, workflowSource{id: body.SourceWorkflowID, name: body.WorkflowName})
		if err != nil {
			writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
			return
		}
		writeJSON(w, 202, map[string]string{"runId": id})
		return
	}

	if body.Task != "" || body.Check != nil {
		if body.Workflow != "" || len(body.Inputs) != 0 {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("project tasks cannot be combined with workflow names or inputs")))
			return
		}
		id, err := s.runner.StartCheckedTask(body.Task, body.Check, body.TargetRoot, body.Project, autoRepair, workflowSource{id: body.SourceWorkflowID, name: body.WorkflowName})
		if err != nil {
			writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"runId": id})
		return
	}
	if body.Workflow != "" {
		if body.Workflow != "app-ideator" {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("unknown SDK workflow %q", body.Workflow)))
			return
		}
		if body.TargetRoot != "" {
			writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("the ideator owns its working folder; targetRoot is unsupported")))
			return
		}
		id, err := s.runner.StartAppIdeator(body.Inputs, body.Goal, body.Project, autoRepair, workflowSource{id: body.SourceWorkflowID, name: body.WorkflowName})
		if err != nil {
			writeJSON(w, workflowErrorStatus(err, http.StatusBadRequest), errBody(err))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"runId": id})
		return
	}

	writeJSON(w, http.StatusBadRequest, errBody(fmt.Errorf("provide workflow steps, a project task, or an explicit SDK workflow name")))
}

// safeID checks that a run ID contains only safe characters (no path traversal).
func safeID(id string) bool {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") || strings.ContainsAny(id, `\:*?"<>|`) {
		return false
	}
	return true
}

// safeRunDir resolves <runsDir>/<runID> and rejects traversal.
func safeRunDir(runsDir, runID string) (string, error) {
	if !safeID(runID) {
		return "", fmt.Errorf("invalid run id")
	}
	return filepath.Join(runsDir, runID), nil
}

func errBody(err error) map[string]any {
	return map[string]any{"error": err.Error()}
}
