package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestSearchKBAllKinds verifies that SearchKB returns results from skills,
// memory, and radar when seeded appropriately.
func TestSearchKBAllKinds(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()

	// Seed a skill.
	skill := Skill{
		ID:          "kb-test-skill-" + suffix,
		Name:        "Swift Concurrency " + suffix,
		Description: "async/await structured concurrency",
		Category:    "ios",
		Instruction: "use async/await",
	}
	if err := st.UpsertSkill(ctx, skill); err != nil {
		t.Fatalf("UpsertSkill: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM skills WHERE id=$1`, skill.ID) })

	// Seed a memory item.
	mem := MemoryItem{
		Kind:   "user-pref",
		Key:    "kb-test-pref-" + suffix,
		Value:  json.RawMessage(`{"prefer":"Swift"}`),
		Source: "human",
	}
	if err := st.UpsertMemory(ctx, mem); err != nil {
		t.Fatalf("UpsertMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteMemory(ctx, mem.Key) })

	// Seed a tech radar entry.
	radar, err := st.CreateTechRadar(ctx, TechRadar{
		Tech:     "SwiftUI " + suffix,
		Quadrant: "languages",
		Ring:     "adopt",
		Note:     "preferred UI framework",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("CreateTechRadar: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM tech_radar WHERE id=$1`, radar.ID) })

	// SearchKB with a query matching the suffix (all three items should appear).
	q := KBQuery{
		Q:     suffix,
		Limit: 50,
	}
	results, err := st.SearchKB(ctx, q)
	if err != nil {
		t.Fatalf("SearchKB: %v", err)
	}

	kinds := map[string]bool{}
	for _, r := range results {
		kinds[r.Kind] = true
	}

	if !kinds["skill"] {
		t.Errorf("SearchKB missing 'skill' results; got kinds=%v results=%d", kinds, len(results))
	}
	if !kinds["memory"] {
		t.Errorf("SearchKB missing 'memory' results; got kinds=%v results=%d", kinds, len(results))
	}
	if !kinds["radar"] {
		t.Errorf("SearchKB missing 'radar' results; got kinds=%v results=%d", kinds, len(results))
	}
}

// TestSearchKBKindsFilter verifies that Kinds=["skill"] returns only skill results.
func TestSearchKBKindsFilter(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()

	// Seed one of each.
	skill := Skill{
		ID:          "kb-filter-skill-" + suffix,
		Name:        "Filter Test Skill " + suffix,
		Description: "testing kinds filter",
		Instruction: "noop",
	}
	if err := st.UpsertSkill(ctx, skill); err != nil {
		t.Fatalf("UpsertSkill: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM skills WHERE id=$1`, skill.ID) })

	mem := MemoryItem{
		Kind:   "project-fact",
		Key:    "kb-filter-mem-" + suffix,
		Value:  json.RawMessage(`{"fact":"filter test"}`),
		Source: "agent",
	}
	if err := st.UpsertMemory(ctx, mem); err != nil {
		t.Fatalf("UpsertMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteMemory(ctx, mem.Key) })

	// Search with Kinds=["skill"] only.
	q := KBQuery{
		Q:     suffix,
		Kinds: []string{"skill"},
		Limit: 50,
	}
	results, err := st.SearchKB(ctx, q)
	if err != nil {
		t.Fatalf("SearchKB: %v", err)
	}

	for _, r := range results {
		if r.Kind != "skill" {
			t.Errorf("expected only 'skill' results but got kind=%q id=%s", r.Kind, r.ID)
		}
	}
	// There should be at least the one skill we seeded.
	found := false
	for _, r := range results {
		if r.ID == skill.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("seeded skill %s not found in filtered results", skill.ID)
	}
}

// TestSearchKBResultFields verifies KBResult has required fields populated.
func TestSearchKBResultFields(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()
	skill := Skill{
		ID:          "kb-fields-" + suffix,
		Name:        "Field Test " + suffix,
		Description: "verifying result fields",
		Instruction: "noop",
	}
	if err := st.UpsertSkill(ctx, skill); err != nil {
		t.Fatalf("UpsertSkill: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM skills WHERE id=$1`, skill.ID) })

	results, err := st.SearchKB(ctx, KBQuery{Q: suffix, Kinds: []string{"skill"}, Limit: 5})
	if err != nil {
		t.Fatalf("SearchKB: %v", err)
	}

	for _, r := range results {
		if r.ID == skill.ID {
			if r.Kind == "" {
				t.Error("KBResult.Kind is empty")
			}
			if r.Summary == "" {
				t.Error("KBResult.Summary is empty")
			}
			if len(r.Payload) == 0 {
				t.Error("KBResult.Payload is empty")
			}
			// Score should be non-negative.
			if r.Score < 0 {
				t.Errorf("KBResult.Score is negative: %f", r.Score)
			}
			return
		}
	}
	t.Errorf("skill %s not found in results", skill.ID)
}
