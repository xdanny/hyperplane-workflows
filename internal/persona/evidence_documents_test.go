package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/rundocs"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// documentRunFixture is a two-step workflow that wrote its note and its review
// to the folder SDK steps actually write to.
func documentRunFixture(t *testing.T) (string, store.Run) {
	t.Helper()
	runsDir := t.TempDir()
	runID := "20260913-note"
	outputs := rundocs.OutputsDir(filepath.Join(runsDir, runID))
	if err := os.MkdirAll(outputs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"note.md":   "# Weekly topic note\n\nHandoffs lose context because the receiving shift reads a ticket, not a conversation.",
		"review.md": "# Check the note\n\nPASS — 121 words against a 120-word instruction.",
	} {
		if err := os.WriteFile(filepath.Join(outputs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := store.Run{
		RunID: runID, DAG: "node-workflow", State: "passed", Goal: "Write this week's topic note",
		Options: store.JSON(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"Write a 120-word note","nodes":[
			{"id":"write","title":"Write the note","kind":"document","instruction":"120 words"},
			{"id":"check","title":"Check the note","kind":"check"}]}}`),
		Doc: store.JSON(`{"sdk":{"phase":"passed","nodes":[
			{"id":"write","phase":"passed","document":{"path":"note.md","sha256":"a"}},
			{"id":"check","phase":"passed","document":{"path":"review.md","sha256":"b"},"findings":["121 words differs from the 120-word instruction"]}]}}`),
	}
	return runsDir, run
}

// S-15: a reviewer told "this run produced no artifact files" while note.md sat
// on the same page cannot honestly judge the note. The evidence now carries the
// documents themselves, captioned by the step that produced them.
func TestRunEvidenceCarriesTheProducedDocuments(t *testing.T) {
	runsDir, run := documentRunFixture(t)
	docs := ReadProducedDocuments(runsDir, run)
	if len(docs) != 2 {
		t.Fatalf("expected both produced documents, got %+v", docs)
	}
	ev := RunEvidence(RunFacts{Run: run, Documents: docs})

	headings := map[string]string{}
	for _, section := range ev.Sections {
		headings[section.Heading] = section.Body
	}
	for _, want := range []string{
		"PRODUCED DOCUMENT: Write the note · note.md",
		"PRODUCED DOCUMENT: Check the note · review.md",
	} {
		if _, ok := headings[want]; !ok {
			t.Fatalf("missing section %q; have %v", want, keys(headings))
		}
	}
	if !strings.Contains(headings["PRODUCED DOCUMENT: Write the note · note.md"], "the receiving shift reads a ticket") {
		t.Fatal("the note's own words must reach the reviewer, not only its name")
	}
	// The honest boundary sentence now names what WAS read, so a reviewer that
	// reports the deliverable as unavailable is contradicting its own prompt.
	if !strings.Contains(ev.Note, "note.md, review.md") {
		t.Fatalf("the note must say which documents were read: %q", ev.Note)
	}
	text := evidenceText(ev)
	if strings.Contains(text, "this run produced no artifact files") {
		t.Fatal("a run with produced documents must not be described as having produced nothing")
	}
}

// The citation refs a reviewer copies come from the evidence headings. One
// shared heading for every step is why six evidence rows all read "SDK STEP
// COMPLETION AND CONFIGURED CHECKS" and located nothing (S-15).
func TestRunEvidenceHeadsEachStepByItsOwnName(t *testing.T) {
	_, run := documentRunFixture(t)
	ev := RunEvidence(RunFacts{Run: run})
	var steps []string
	for _, section := range ev.Sections {
		if strings.HasPrefix(section.Heading, "STEP: ") {
			steps = append(steps, section.Heading)
		}
		if section.Heading == "SDK STEP COMPLETION AND CONFIGURED CHECKS" {
			t.Fatal("the per-step evidence must no longer share one heading")
		}
	}
	if strings.Join(steps, "|") != "STEP: Write the note|STEP: Check the note" {
		t.Fatalf("step sections %v", steps)
	}
	// Splitting the sections must not lose what each step actually recorded.
	text := evidenceText(ev)
	for _, want := range []string{
		"step write (Write the note): phase=passed",
		"step check (Check the note): phase=passed; model review (fallible)",
		"121 words differs from the 120-word instruction",
		"Frozen instruction: Write a 120-word note",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
}

// A run whose folder has been cleaned up must say so rather than letting the
// reviewer pass or fail work it never saw.
func TestRunEvidenceSaysWhenNoDocumentCouldBeRead(t *testing.T) {
	_, run := documentRunFixture(t)
	ev := RunEvidence(RunFacts{Run: run})
	if !strings.Contains(ev.Note, "No document this run produced could be read") {
		t.Fatalf("the absence must be stated: %q", ev.Note)
	}
	if !strings.Contains(evidenceText(ev), "this run produced no artifact files") {
		t.Fatal("an empty run must still report an empty inventory")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
