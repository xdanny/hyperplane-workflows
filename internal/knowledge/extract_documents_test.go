package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/rundocs"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// D-14: extraction saw a 400-character checkpoint excerpt and a file NAME, and
// said so in its own rationale — "no delivered summary is shown" — while the
// summary sat in the run's outputs folder. Naming a reusable method from work
// nobody read is a guess wearing a confidence score.
func TestExtractPromptCarriesTheDeliveredDocuments(t *testing.T) {
	run := sdkFixture("20260913-scan", "passed", "run")
	run.Options = store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"Compare rivals using consistent axes","nodes":[
		{"id":"gather","title":"Gather the rivals","kind":"task","instruction":"Find rival offerings"},
		{"id":"verify","title":"Check the citations","kind":"check","instruction":"Check claim citations"}]}}`)
	run.Doc = store.JSON(`{"sdk":{"phase":"passed","nodes":[
		{"id":"gather","phase":"passed","verification":"not_requested","document":{"path":"scan.md","sha256":"a"},"findings":["three rivals, all subscription-priced"]},
		{"id":"verify","phase":"passed","findings":["every claim is cited"]}]}}`)

	runDir := t.TempDir()
	outputs := rundocs.OutputsDir(runDir)
	if err := os.MkdirAll(outputs, 0o755); err != nil {
		t.Fatal(err)
	}
	const delivered = "# Rival scan\n\nAll three rivals price per seat per month; none charges for storage."
	if err := os.WriteFile(filepath.Join(outputs, "scan.md"), []byte(delivered), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := inspectSDKExtraction(run)
	if err != nil {
		t.Fatal(err)
	}
	documents := rundocs.Read(runDir, run)
	if len(documents) != 1 {
		t.Fatalf("expected the delivered document, got %+v", documents)
	}
	prompt := buildExtractPrompt(run, snapshot, documents)
	for _, want := range []string{
		"=== DELIVERED DOCUMENTS ===",
		"--- Gather the rivals · scan.md ---",
		"none charges for storage",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q in prompt:\n%s", want, prompt)
		}
	}

	// And the honest opposite: a cleaned-up folder must not let the prompt imply
	// the delivery was read.
	bare := buildExtractPrompt(run, snapshot, nil)
	if !strings.Contains(bare, "None could be read.") {
		t.Fatalf("an unreadable delivery must be stated:\n%s", bare)
	}
	if strings.Contains(bare, "none charges for storage") {
		t.Fatal("no document was read, so none may be quoted")
	}
}
