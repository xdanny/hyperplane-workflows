package seed_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// writeSkillDef writes a minimal skills/<id>.json def file into dir/skills,
// creating the subdirectory if needed. instruction must be long enough to
// pass the "not too thin to be worth seeding" convention used elsewhere, and
// must not reference docs/ so buildSkillMeta computes no "doc" key from it —
// exactly the shape of the in-repo starter defs this bug affects.
func writeSkillDef(t *testing.T, dir, id, reasoning string, metadata json.RawMessage) {
	t.Helper()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillsDir, err)
	}
	def := map[string]any{
		"id":          id,
		"name":        "Test Merge Skill",
		"description": "Exercises the seed-layer metadata merge on reseed.",
		"instruction": "This is a long, self-contained instruction that stands on its own with no external doc pointers and is well over two hundred characters so it clears the seed def well-formedness bar used elsewhere in this package's tests.",
	}
	if reasoning != "" {
		def["reasoning"] = reasoning
	}
	if len(metadata) > 0 {
		var m json.RawMessage = metadata
		def["metadata"] = m
	}
	b, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal def: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, id+".json"), b, 0o644); err != nil {
		t.Fatalf("write def: %v", err)
	}
}

// TestFromDefsDir_MergesSkillMetadataOnReseed is the regression guard for the
// "reseed silently wipes docs" bug: store.UpsertSkill overwrites metadata
// wholesale, so re-seeding an existing Orchestra-seeded install used to wipe
// metadata.doc (the inlined playbook resolveSkillDocs feeds to codex) and any
// other key the current def file doesn't know about, plus the top-level
// reasoning column. FromDefsDir must now merge conservatively: existing keys
// survive unless the def explicitly provides that key.
func TestFromDefsDir_MergesSkillMetadataOnReseed(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set — skipping seed round-trip")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Skipf("store unavailable: %v", err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const id = "test-metadata-merge-skill"

	// Seed a pre-existing row simulating an Orchestra-origin skill: an inlined
	// doc, an unrelated metadata key (e.g. an extracted skill's status), and a
	// reasoning level — none of which the in-repo def file below will set.
	if err := st.UpsertSkill(ctx, store.Skill{
		ID:          id,
		Name:        "Orchestra Skill",
		Instruction: "original orchestra instruction",
		Reasoning:   "high",
		Metadata:    json.RawMessage(`{"doc":"X","status":"extracted"}`),
	}); err != nil {
		t.Fatalf("seed existing skill: %v", err)
	}

	tmp := t.TempDir()

	// Step 1: reseed from a def with NO metadata and NO reasoning (the shape of
	// the in-repo starter defs). doc and status must survive; reasoning must
	// survive too, since the def doesn't set it.
	writeSkillDef(t, tmp, id, "", nil)
	if _, err := seed.FromDefsDir(ctx, st, tmp); err != nil {
		t.Fatalf("FromDefsDir (no metadata, no reasoning): %v", err)
	}
	sk, err := st.GetSkill(ctx, id)
	if err != nil {
		t.Fatalf("GetSkill after reseed: %v", err)
	}
	var meta map[string]string
	if err := json.Unmarshal(sk.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["doc"] != "X" {
		t.Errorf("metadata.doc = %q after reseed without a def-provided doc, want survival of %q", meta["doc"], "X")
	}
	if meta["status"] != "extracted" {
		t.Errorf("metadata.status = %q after reseed, want survival of %q", meta["status"], "extracted")
	}
	if sk.Reasoning != "high" {
		t.Errorf("reasoning = %q after reseed without a def-provided reasoning, want survival of %q", sk.Reasoning, "high")
	}
	// Also confirm the reseeded instruction actually took — this proves the
	// merge didn't just skip the upsert entirely.
	if sk.Instruction == "original orchestra instruction" {
		t.Errorf("instruction did not update on reseed — merge should not block the rest of the upsert")
	}

	// Step 2: reseed again, this time from a def that explicitly sets both an
	// overriding metadata.doc AND an explicit reasoning. Both must win, but
	// the untouched "status" key must still survive.
	writeSkillDef(t, tmp, id, "low", json.RawMessage(`{"doc":"Y"}`))
	if _, err := seed.FromDefsDir(ctx, st, tmp); err != nil {
		t.Fatalf("FromDefsDir (explicit doc + reasoning): %v", err)
	}
	sk2, err := st.GetSkill(ctx, id)
	if err != nil {
		t.Fatalf("GetSkill after second reseed: %v", err)
	}
	var meta2 map[string]string
	if err := json.Unmarshal(sk2.Metadata, &meta2); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta2["doc"] != "Y" {
		t.Errorf("metadata.doc = %q after reseed with an explicit doc, want overwrite to %q", meta2["doc"], "Y")
	}
	if meta2["status"] != "extracted" {
		t.Errorf("metadata.status = %q after second reseed, want survival of %q", meta2["status"], "extracted")
	}
	if sk2.Reasoning != "low" {
		t.Errorf("reasoning = %q after reseed with an explicit reasoning, want overwrite to %q", sk2.Reasoning, "low")
	}
}
