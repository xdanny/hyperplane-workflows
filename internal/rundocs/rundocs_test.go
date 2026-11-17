package rundocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// fixture writes an SDK run whose checkpoint records documents, plus the files
// themselves under the folder a step actually writes to.
func fixture(t *testing.T, files map[string]string) (string, store.Run) {
	t.Helper()
	dir := t.TempDir()
	outputs := OutputsDir(dir)
	for path, body := range files {
		full := filepath.Join(outputs, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := store.Run{
		RunID: "20260913-abc", DAG: "node-workflow", State: "passed",
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[
			{"id":"write","title":"Write the note","kind":"document"},
			{"id":"check","title":"Check the note","kind":"check"}]}}`),
		Doc: store.JSON(`{"sdk":{"phase":"passed","nodes":[
			{"id":"check","phase":"passed","document":{"path":"review.md","sha256":"b"}},
			{"id":"write","phase":"passed","document":{"path":"note.md","sha256":"a"},
			 "files":[{"path":"build/totals.csv","sha256":"c"},{"path":"../escape.md","sha256":"d"}]}]}}`),
	}
	return dir, run
}

// The whole point of the package: the documents a run wrote are found, in
// declared-step order, captioned by the step that produced them.
func TestReadFindsSDKOutputsInDeclaredStepOrder(t *testing.T) {
	dir, run := fixture(t, map[string]string{
		"note.md":          "# Weekly note\n\nHandoffs lose context.",
		"review.md":        "# Check the note\n\nPASS.",
		"build/totals.csv": "a,b\n1,2\n",
	})
	docs := Read(dir, run)
	var captions []string
	for _, doc := range docs {
		captions = append(captions, doc.Caption())
	}
	want := []string{
		"Write the note · note.md",
		"Write the note · build/totals.csv",
		"Check the note · review.md",
	}
	if strings.Join(captions, "|") != strings.Join(want, "|") {
		t.Fatalf("captions %v, want %v", captions, want)
	}
	if docs[0].Text != "# Weekly note\n\nHandoffs lose context." {
		t.Fatalf("the note's own text is what a reader needs: %q", docs[0].Text)
	}
	for _, doc := range docs {
		if doc.Truncated {
			t.Fatalf("%s is short and must not be reported as truncated", doc.Path)
		}
	}
}

// A checkpoint is a record, not a capability: a path that climbs out of the
// run's own outputs folder is refused rather than read.
func TestReadRefusesPathsOutsideTheRunOutputs(t *testing.T) {
	dir, run := fixture(t, map[string]string{"note.md": "kept"})
	if err := os.WriteFile(filepath.Join(dir, "nodes", "delivery", "escape.md"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, doc := range Read(dir, run) {
		if strings.Contains(doc.Text, "SECRET") || strings.Contains(doc.Path, "..") {
			t.Fatalf("traversal escaped the outputs folder: %+v", doc)
		}
	}
}

// A bound that bites must say so, in the record itself, or a reader judges a
// fragment believing it read the whole thing.
func TestReadBoundsOneDocumentAndSaysSo(t *testing.T) {
	long := strings.Repeat("x", MaxBytes+500)
	dir, run := fixture(t, map[string]string{"note.md": long, "review.md": "short"})
	docs := Read(dir, run)
	if len(docs) < 2 {
		t.Fatalf("both documents should still be offered: %d", len(docs))
	}
	if !docs[0].Truncated || len(docs[0].Text) > MaxBytes {
		t.Fatalf("long document not bounded: truncated=%v len=%d", docs[0].Truncated, len(docs[0].Text))
	}
	if docs[len(docs)-1].Truncated {
		t.Fatal("a short document must not be marked truncated")
	}
}

// A run with no recorded documents yields nothing — never a guess from walking
// the folder, which is what would let an unrecorded stray file become evidence.
func TestReadYieldsNothingWithoutACheckpoint(t *testing.T) {
	dir, run := fixture(t, map[string]string{"note.md": "kept"})
	run.Doc = store.JSON(`{}`)
	if docs := Read(dir, run); len(docs) != 0 {
		t.Fatalf("expected no documents, got %+v", docs)
	}
	if docs := Read("", run); len(docs) != 0 {
		t.Fatalf("no runs dir means no documents, got %+v", docs)
	}
}
