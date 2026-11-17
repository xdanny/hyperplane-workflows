package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestMemoryUpsertAndGet verifies UpsertMemory + GetMemory round-trip + update.
func TestMemoryUpsertAndGet(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	key := "test.memory.key." + uniq()
	t.Cleanup(func() { _ = st.DeleteMemory(ctx, key) })

	item := MemoryItem{
		Kind:   "user-pref",
		Key:    key,
		Value:  JSON(`{"theme":"dark","lang":"en"}`),
		Source: "human",
	}
	if err := st.UpsertMemory(ctx, item); err != nil {
		t.Fatalf("UpsertMemory: %v", err)
	}

	got, err := st.GetMemory(ctx, key)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got.Kind != "user-pref" {
		t.Errorf("Kind: got %s want user-pref", got.Kind)
	}
	if got.Source != "human" {
		t.Errorf("Source: got %s want human", got.Source)
	}
	if got.ID == "" {
		t.Errorf("ID should not be empty")
	}

	// Update via upsert (same key, different value).
	item.Value = JSON(`{"theme":"light"}`)
	if err := st.UpsertMemory(ctx, item); err != nil {
		t.Fatalf("UpsertMemory (update): %v", err)
	}
	got2, err := st.GetMemory(ctx, key)
	if err != nil {
		t.Fatalf("GetMemory after update: %v", err)
	}
	var v map[string]string
	json.Unmarshal(got2.Value, &v)
	if v["theme"] != "light" {
		t.Errorf("expected updated theme=light, got %v", v)
	}
}

// TestMemoryListAndDelete verifies ListMemory + DeleteMemory.
func TestMemoryListAndDelete(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	key1 := "pref.a." + uniq()
	key2 := "fact.b." + uniq()
	t.Cleanup(func() {
		_ = st.DeleteMemory(ctx, key1)
		_ = st.DeleteMemory(ctx, key2)
	})

	_ = st.UpsertMemory(ctx, MemoryItem{Kind: "user-pref", Key: key1, Value: JSON(`{}`), Source: "human"})
	_ = st.UpsertMemory(ctx, MemoryItem{Kind: "project-fact", Key: key2, Value: JSON(`{}`), Source: "agent"})

	prefs, err := st.ListMemory(ctx, "user-pref", 50)
	if err != nil {
		t.Fatalf("ListMemory user-pref: %v", err)
	}
	found := false
	for _, m := range prefs {
		if m.Key == key1 {
			found = true
		}
	}
	if !found {
		t.Errorf("%s not found in ListMemory(user-pref)", key1)
	}

	// Deleting the item should work.
	if err := st.DeleteMemory(ctx, key1); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	// Second delete → ErrNotFound.
	if err := st.DeleteMemory(ctx, key1); err != ErrNotFound {
		t.Errorf("expected ErrNotFound on second delete, got %v", err)
	}

	// GetMemory should return ErrNotFound.
	_, err = st.GetMemory(ctx, key1)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// TestMemoryGetNotFound verifies ErrNotFound for missing keys.
func TestMemoryGetNotFound(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	_, err := st.GetMemory(ctx, "no-such-key-"+uniq())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
