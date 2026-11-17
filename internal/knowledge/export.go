package knowledge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/journey"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ExportRun writes an immutable SDK receipt beneath targetRoot/knowledge/runs.
// It does not copy output files, read external paths, or change Git state.
// Identical exports are idempotent; different existing contents are preserved.
func ExportRun(ctx context.Context, targetRoot string, run store.Run, outcome *store.Decision) (string, error) {
	files, err := exportFiles(run, outcome)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(targetRoot)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return "", fmt.Errorf("open export destination: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll("knowledge/runs", 0700); err != nil {
		return "", fmt.Errorf("prepare export destination: %w", err)
	}
	runs, err := root.OpenRoot("knowledge/runs")
	if err != nil {
		return "", err
	}
	defer runs.Close()
	idHash := sha256.Sum256([]byte(run.RunID))
	name := fmt.Sprintf("%s-%x", slugify(run.RunID), idHash[:6])
	result := filepath.Join(absolute, "knowledge", "runs", name)
	if _, err := runs.Lstat(name); err == nil {
		return result, matchExport(runs, name, files)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	tmp := ".export-" + rand.Text()
	if err := runs.Mkdir(tmp, 0700); err != nil {
		return "", err
	}
	defer runs.RemoveAll(tmp)
	for filename, contents := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f, err := runs.OpenFile(filepath.Join(tmp, filename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", err
		}
		_, writeErr := f.Write(contents)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := runs.Rename(tmp, name); err != nil {
		// Another export may have published the same receipt concurrently.
		if matchErr := matchExport(runs, name, files); matchErr == nil {
			return result, nil
		}
		return "", fmt.Errorf("publish SDK export: %w", err)
	}
	return result, nil
}

func exportFiles(run store.Run, outcome *store.Decision) (map[string][]byte, error) {
	if run.RunID == "" || (run.State != "passed" && run.State != "failed" && run.State != "interrupted") {
		return nil, fmt.Errorf("export requires a finished run")
	}
	var launch struct {
		Runtime string          `json:"runtime"`
		Request json.RawMessage `json:"request"`
	}
	var doc struct {
		SDK json.RawMessage `json:"sdk"`
	}
	if json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" || json.Unmarshal(run.Doc, &doc) != nil {
		return nil, fmt.Errorf("run has no saved SDK request and checkpoint")
	}
	for _, raw := range []json.RawMessage{launch.Request, doc.SDK} {
		raw = bytes.TrimSpace(raw)
		if len(raw) < 2 || len(raw) > 1_000_000 || raw[0] != '{' || !json.Valid(raw) {
			return nil, fmt.Errorf("SDK request or checkpoint is absent, invalid or exceeds 1 MB")
		}
	}
	var request struct{ Workflow string }
	var checkpoint struct{ Phase string }
	if json.Unmarshal(launch.Request, &request) != nil || json.Unmarshal(doc.SDK, &checkpoint) != nil || request.Workflow == "" || checkpoint.Phase != run.State {
		return nil, fmt.Errorf("SDK checkpoint does not match the finished run")
	}
	if request.Workflow != "node-workflow" && request.Workflow != "checked-task" && request.Workflow != "app-ideator" {
		return nil, fmt.Errorf("unsupported SDK workflow in export")
	}
	if outcome != nil {
		bound := false
		for _, ref := range outcome.Refs {
			bound = bound || ref == "run:"+run.RunID
		}
		if !bound {
			return nil, fmt.Errorf("human outcome does not reference this run")
		}
	}
	snapshot := struct {
		Version       int             `json:"version"`
		RunID         string          `json:"runId"`
		Project       string          `json:"project,omitempty"`
		ContinuedFrom string          `json:"continuedFrom,omitempty"`
		DeliveryRoot  string          `json:"deliveryRoot"`
		Goal          string          `json:"goal"`
		State         string          `json:"state"`
		Rehearsal     bool            `json:"rehearsal,omitempty"`
		RecordedAt    time.Time       `json:"recordedAt"`
		Request       json.RawMessage `json:"request"`
		Checkpoint    json.RawMessage `json:"checkpoint"`
		Outcome       *store.Decision `json:"humanOutcome,omitempty"`
	}{Version: 1, RunID: run.RunID, Project: run.Project, ContinuedFrom: store.SDKContinuationSource(run), DeliveryRoot: store.SDKDeliveryRoot(run), Goal: run.Goal, State: run.State, Rehearsal: store.IsRehearsal(run), RecordedAt: run.UpdatedAt, Request: launch.Request, Checkpoint: doc.SDK, Outcome: outcome}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil || len(raw) > 4_000_000 {
		return nil, fmt.Errorf("SDK export could not be encoded within 4 MB")
	}
	checks := journey.CriteriaStatusForRun(run)
	var summary strings.Builder
	fmt.Fprintf(&summary, "# SDK run receipt\n\nRun: %s\n\nGoal: %s\n\nRecorded state: %s\n\n", run.RunID, run.Goal, run.State)
	if snapshot.Rehearsal {
		summary.WriteString("This is a rehearsal receipt; it does not establish that the work ran.\n\n")
	}
	if len(checks) == 0 {
		summary.WriteString("No extra checks were configured. Completion does not establish independently checked correctness.\n\n")
	}
	for _, check := range checks {
		fmt.Fprintf(&summary, "- %s (%s): %s\n", check.Title, check.Basis, check.ProofLabel())
	}
	if outcome != nil {
		fmt.Fprintf(&summary, "\nHuman outcome: %s. This is a person's decision, separate from the check results.\n", outcome.Decision)
	}
	summary.WriteString("\n[snapshot.json](snapshot.json) contains the saved request and checkpoint. File inventories are recorded references; produced files and external sources are not copied or reverified by this export.\n")
	return map[string][]byte{"snapshot.json": append(raw, '\n'), "README.md": []byte(summary.String())}, nil
}

func matchExport(root *os.Root, name string, files map[string][]byte) error {
	conflict := fmt.Errorf("export destination already contains different data; choose a different target to preserve both snapshots")
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() {
		return conflict
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return conflict
	}
	defer dir.Close()
	folder, err := dir.Open(".")
	if err != nil {
		return conflict
	}
	entries, readErr := folder.ReadDir(len(files) + 1)
	closeErr := folder.Close()
	if (readErr != nil && !errors.Is(readErr, io.EOF)) || closeErr != nil || len(entries) != len(files) {
		return conflict
	}
	for filename, want := range files {
		info, err := dir.Lstat(filename)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(want)) {
			return conflict
		}
		f, err := dir.Open(filename)
		if err != nil {
			return conflict
		}
		got, readErr := io.ReadAll(io.LimitReader(f, int64(len(want))+1))
		closeErr := f.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(want, got) {
			return conflict
		}
	}
	return nil
}
