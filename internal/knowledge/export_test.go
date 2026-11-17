package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func exportFixture() store.Run {
	return store.Run{
		RunID: "20260907-task", Goal: "Draft a report", State: "passed", UpdatedAt: time.Unix(100, 0).UTC(),
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"checked-task","instruction":"Draft a report","model":"fixture"},"notExported":"private-option"}`),
		Doc:     store.JSON(`{"sdk":{"phase":"passed","verification":"not_requested","text":"Draft complete"},"notExported":"private-document"}`),
	}
}

func TestExportSDKReceiptPreservesExistingWorkAndPinsOnlyItsDecision(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0700); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(gitDir, "index")
	if err := os.WriteFile(index, []byte("user staged work"), 0600); err != nil {
		t.Fatal(err)
	}
	run := exportFixture()
	outcome := &store.Decision{ID: "decision", Decision: "accepted", Refs: []string{"run:" + run.RunID}}
	path, err := ExportRun(t.Context(), dir, run, outcome)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(path, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		RunID        string
		Request      json.RawMessage
		Checkpoint   json.RawMessage
		HumanOutcome store.Decision
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.RunID != run.RunID || receipt.HumanOutcome.ID != outcome.ID || !bytes.Contains(receipt.Checkpoint, []byte(`"not_requested"`)) || bytes.Contains(raw, []byte("private-")) {
		t.Fatalf("wrong exported receipt: %s", raw)
	}
	summary, _ := os.ReadFile(filepath.Join(path, "README.md"))
	if !bytes.Contains(summary, []byte("No extra checks were configured")) || !bytes.Contains(summary, []byte("Human outcome: accepted")) {
		t.Fatalf("summary: %s", summary)
	}
	gotIndex, _ := os.ReadFile(index)
	if string(gotIndex) != "user staged work" {
		t.Fatal("export changed Git state")
	}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); !os.IsNotExist(err) {
		t.Fatal("export created a Git commit")
	}
	if again, err := ExportRun(t.Context(), dir, run, outcome); err != nil || again != path {
		t.Fatalf("identical export: %q %v", again, err)
	}
	outcome.Decision = "rejected"
	if _, err := ExportRun(t.Context(), dir, run, outcome); err == nil {
		t.Fatal("changed export overwrote earlier decision")
	}
	after, _ := os.ReadFile(filepath.Join(path, "snapshot.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("existing receipt changed")
	}
}

func TestExportRejectsUnavailableOrUnboundSDKRecordsBeforeWriting(t *testing.T) {
	for _, mode := range []string{"active", "legacy", "null", "mismatch", "oversize", "foreign-outcome", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			run := exportFixture()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var outcome *store.Decision
			switch mode {
			case "active":
				run.State = "running"
			case "legacy":
				run.Options = store.JSON(`{"runtime":"graph"}`)
			case "null":
				run.Doc = store.JSON(`{"sdk":null}`)
			case "mismatch":
				run.Doc = store.JSON(`{"sdk":{"phase":"running"}}`)
			case "oversize":
				run.Doc = store.JSON(`{"sdk":{"phase":"passed","text":"` + strings.Repeat("a", 1_000_000) + `"}}`)
			case "foreign-outcome":
				outcome = &store.Decision{Decision: "accepted", Refs: []string{"run:another-run"}}
			case "cancelled":
				cancel()
			}
			if _, err := ExportRun(ctx, dir, run, outcome); err == nil {
				t.Fatal("invalid export reported success")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("invalid export wrote files")
			}
		})
	}
}

func TestExportContainsPathsAndReportsWriteErrors(t *testing.T) {
	for _, mode := range []string{"missing-target", "file-target", "escaping-symlink", "conflicting-file"} {
		t.Run(mode, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			target := dir
			switch mode {
			case "missing-target":
				target = filepath.Join(dir, "missing")
			case "file-target":
				target = filepath.Join(dir, "file")
				if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "escaping-symlink":
				if err := os.Symlink(outside, filepath.Join(dir, "knowledge")); err != nil {
					t.Fatal(err)
				}
			case "conflicting-file":
				if err := os.WriteFile(filepath.Join(dir, "knowledge"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ExportRun(t.Context(), target, exportFixture(), nil); err == nil {
				t.Fatal("write error reported as success")
			}
			entries, _ := os.ReadDir(outside)
			if len(entries) != 0 {
				t.Fatal("export escaped destination")
			}
		})
	}
	run := exportFixture()
	run.RunID = "../../outside"
	dir := t.TempDir()
	path, err := ExportRun(t.Context(), dir, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(dir, "knowledge", "runs"), path)
	if err != nil || !filepath.IsLocal(rel) || strings.Contains(rel, string(filepath.Separator)) {
		t.Fatalf("unsafe export path %q", path)
	}
}

func TestConcurrentSDKExportsPublishOnlyOneCompleteReceipt(t *testing.T) {
	dir := t.TempDir()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := ExportRun(t.Context(), dir, exportFixture(), nil); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	entries, err := os.ReadDir(filepath.Join(dir, "knowledge", "runs"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("exports: %v %v", entries, err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "knowledge", "runs", entries[0].Name()))
	if err != nil || len(files) != 2 {
		t.Fatalf("incomplete export: %v %v", files, err)
	}
}
