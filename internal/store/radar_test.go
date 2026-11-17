package store

import (
	"context"
	"testing"
)

func TestTechRadarUpsert(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	tech := "test-tech-" + uniq()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM tech_radar WHERE tech=$1`, tech)
	})

	entry := TechRadar{
		Tech:        tech,
		Quadrant:    "tools",
		Ring:        "trial",
		SkillRef:    "golang-backend",
		Description: "A test technology entry.",
		Note:        "Test note.",
		Status:      "active",
	}

	// Insert via Upsert.
	got, err := st.UpsertTechRadarEntry(ctx, entry)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if got.Tech != tech {
		t.Fatalf("tech: got %q want %q", got.Tech, tech)
	}
	if got.SkillRef != "golang-backend" {
		t.Fatalf("skillRef: got %q want %q", got.SkillRef, "golang-backend")
	}
	if got.Status != "active" {
		t.Fatalf("status: got %q want %q", got.Status, "active")
	}

	// GetTechRadarEntry by ID.
	fetched, err := st.GetTechRadarEntry(ctx, got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.Tech != tech {
		t.Fatalf("get tech: got %q", fetched.Tech)
	}

	// Upsert again with ring change — should update, not insert duplicate.
	entry.Ring = "adopt"
	entry.Note = "Updated note."
	got2, err := st.UpsertTechRadarEntry(ctx, entry)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got2.ID != got.ID {
		t.Fatalf("upsert changed ID: %s → %s", got.ID, got2.ID)
	}
	if got2.Ring != "adopt" {
		t.Fatalf("ring not updated: got %q", got2.Ring)
	}

	// ListTechRadar should include the entry.
	list, err := st.ListTechRadar(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, e := range list {
		if e.Tech == tech {
			found = true
			if e.SkillRef != "golang-backend" {
				t.Fatalf("list skillRef: got %q", e.SkillRef)
			}
		}
	}
	if !found {
		t.Fatalf("entry not in list (list has %d entries)", len(list))
	}
}

func TestTechRadarGetNotFound(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	// Use a well-formed UUID that does not exist.
	_, err := st.GetTechRadarEntry(ctx, "00000000-0000-0000-0000-000000000000")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
