package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// TestImportIsIdempotent asserts the seed rule: re-importing an unchanged
// charter is a no-op, and an amended charter lands with a new version. The
// repo stays the source of truth, and `make seed` stays safe to run at will.
func TestImportIsIdempotent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	dir := t.TempDir()

	id := fmt.Sprintf("seedy-%d", time.Now().UnixNano())
	charter := strings.Replace(testCharter, "# Testy — senior reviewer", "# Seedy — senior reviewer", 1)
	writeCharter(t, dir, id+".md", charter)
	// The directory's own README documents the roster; it is not a reviewer.
	writeCharter(t, dir, readmeName, "# Audit personas\n\nNot a reviewer.\n")
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM personas WHERE id=$1`, id) })

	ids, err := Import(ctx, st, dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("imported %v, want just %q — README.md must not register a reviewer", ids, id)
	}

	first, err := st.GetPersona(ctx, id)
	if err != nil {
		t.Fatalf("get persona: %v", err)
	}
	if first.Name != "Seedy — senior reviewer" {
		t.Errorf("name = %q", first.Name)
	}
	if first.Source != "personas/"+id+".md" {
		t.Errorf("source = %q — it must point at the file a human edits", first.Source)
	}
	var comp Competence
	if err := json.Unmarshal(first.Competence, &comp); err != nil {
		t.Fatalf("competence is not readable json: %v", err)
	}
	if len(comp.Skills) != 1 || comp.Skills[0] != "ai-engineering" {
		t.Errorf("competence.skills = %v", comp.Skills)
	}

	// Re-seed the SAME content: nothing about the row's identity moves.
	if _, err := Import(ctx, st, dir); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	again, err := st.GetPersona(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != first.Version {
		t.Errorf("re-seeding an unchanged charter changed its version: %q → %q", first.Version, again.Version)
	}
	if !again.CreatedAt.Equal(first.CreatedAt) {
		t.Error("re-seeding must not reset created_at — 'registered since' is a fact worth keeping")
	}

	// Amend the charter: the new content lands, with a new version.
	writeCharter(t, dir, id+".md", charter+"\n- **Skills**: `data-visualization`.\n")
	if _, err := Import(ctx, st, dir); err != nil {
		t.Fatalf("import amended: %v", err)
	}
	amended, err := st.GetPersona(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if amended.Version == first.Version {
		t.Error("an amended charter must land with a new version")
	}
	if !strings.Contains(amended.Charter, "data-visualization") {
		t.Error("the amended charter body was not refreshed")
	}
}

// TestImportMissingDirIsNotAnError — a clone without personas/ simply has no
// registered reviewers; the seed must not fail over it.
func TestImportMissingDirIsNotAnError(t *testing.T) {
	st := testStore(t)
	ids, err := Import(context.Background(), st, t.TempDir()+"/nope")
	if err != nil {
		t.Fatalf("a missing personas dir must be tolerated: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("registered %v from a directory that does not exist", ids)
	}
}

// TestSkillEvidenceIsHonestAboutThinSupport asserts the skill-target evidence
// carries what the SKILL-REVIEW PROTOCOL asks for by name — the instruction,
// the support count, the pinned quotes — and labels a self-reported confidence
// as the model's opinion rather than a measurement.
func TestSkillEvidenceIsHonestAboutThinSupport(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id := fmt.Sprintf("ev-skill-%d", time.Now().UnixNano())

	meta, _ := json.Marshal(map[string]any{
		"status":      "draft",
		"sourceRuns":  []string{"run-a"},
		"supportRuns": 1,
		"confidence":  0.9,
		"evidence":    []map[string]string{{"runId": "run-a", "nodeId": "concepts-gate", "quote": "tied to direct quotations"}},
	})
	if err := st.UpsertSkill(ctx, store.Skill{
		ID: id, Name: "Learned method", Instruction: "Rank the problems using consistent criteria.",
		Metadata: meta,
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), id) })

	ev, err := SkillEvidence(ctx, st, id)
	if err != nil {
		t.Fatalf("SkillEvidence: %v", err)
	}
	joined := ev.Title + "\n" + ev.Note
	for _, s := range ev.Sections {
		joined += "\n" + s.Heading + "\n" + s.Body
	}
	for _, want := range []string{
		"DRAFT",                              // its status, plainly
		"Rank the problems using consistent", // the instruction in full
		"treat it as an anecdote",            // what support = 1 actually means
		"not a measurement",                  // the confidence scalar, labelled
		"tied to direct quotations",          // the pinned quote
		"concepts-gate",                      // where the quote came from
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("skill evidence is missing %q", want)
		}
	}
}

// TestSkillEvidenceSaysWhenNothingIsPinned — a draft extracted before evidence
// pinning existed must say so rather than showing an empty block. The absence
// is itself a fact a reviewer needs.
func TestSkillEvidenceSaysWhenNothingIsPinned(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id := fmt.Sprintf("ev-bare-%d", time.Now().UnixNano())
	if err := st.UpsertSkill(ctx, store.Skill{ID: id, Name: "Bare", Instruction: "Do the thing."}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSkill(context.Background(), id) })

	ev, err := SkillEvidence(ctx, st, id)
	if err != nil {
		t.Fatal(err)
	}
	var pinned string
	for _, s := range ev.Sections {
		if strings.Contains(s.Heading, "PINNED EVIDENCE") {
			pinned = s.Body
		}
	}
	if !strings.Contains(pinned, "No pinned evidence quotes") {
		t.Errorf("an unpinned skill must say so: %q", pinned)
	}
}

// TestRunEvidenceReportsNoFailuresHonestly — a clean run must not be handed to
// a reviewer as though a failures digest were missing.
func TestRunEvidenceReportsNoFailuresHonestly(t *testing.T) {
	ev := RunEvidence(RunFacts{
		Run: store.Run{RunID: "r1", DAG: "hello", State: "passed", Goal: "ship it"},
	})
	var failures, proof string
	for _, s := range ev.Sections {
		if strings.HasPrefix(s.Heading, "WHAT FAILED") {
			failures = s.Body
		}
		if strings.HasPrefix(s.Heading, "SDK RUN COMPLETION") {
			proof = s.Body
		}
	}
	if !strings.Contains(failures, "No failed SDK checkpoint findings") {
		t.Errorf("a clean run must say so: %q", failures)
	}
	if !strings.Contains(proof, "No recorded SDK step evidence") {
		t.Errorf("a run with no SDK evidence must say so rather than showing an empty list: %q", proof)
	}
}

func writeCharter(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
