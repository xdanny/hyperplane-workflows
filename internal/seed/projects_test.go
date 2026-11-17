package seed_test

import (
	"context"
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// projectSeedStore opens the (redirected) test database, or skips.
func projectSeedStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping project seed test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Skipf("store unavailable: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestDefaultProjectsSeeded verifies the example projects land as registered
// rows with their description and the display name EnsureProject derives from
// the tag — so a fresh install opens on real domains rather than an empty
// Projects surface.
func TestDefaultProjectsSeeded(t *testing.T) {
	st := projectSeedStore(t)
	ctx := context.Background()

	if err := seed.Defaults(ctx, st); err != nil {
		t.Fatalf("Defaults: %v", err)
	}

	wantNames := map[string]string{
		"taxi-analytics":      "Taxi Analytics",
		"service-reliability": "Service Reliability",
	}
	if len(seed.DefaultProjects) != len(wantNames) {
		t.Errorf("DefaultProjects has %d entries, want %d", len(seed.DefaultProjects), len(wantNames))
	}
	for _, p := range seed.DefaultProjects {
		got, err := st.GetProject(ctx, p.ID)
		if err != nil {
			t.Errorf("GetProject(%q) after seed: %v", p.ID, err)
			continue
		}
		if got.Summary != p.Description {
			t.Errorf("project %q: summary = %q, want %q", p.ID, got.Summary, p.Description)
		}
		if want, ok := wantNames[p.ID]; ok && got.Name != want {
			t.Errorf("project %q: name = %q, want %q (EnsureProject's INITCAP of the tag)", p.ID, got.Name, want)
		}
		if got.Status != "active" {
			t.Errorf("project %q: status = %q, want active", p.ID, got.Status)
		}
	}
}

// TestDefaultProjectsSeedIsIdempotent reseeds and asserts nothing moves: the row
// is not recreated (created_at is stable) and re-registering an already-described
// project is not treated as an edit (updated_at is stable, which is what keeps
// the activity-ordered Projects surface from reshuffling on every `make seed`).
func TestDefaultProjectsSeedIsIdempotent(t *testing.T) {
	st := projectSeedStore(t)
	ctx := context.Background()

	if err := seed.Defaults(ctx, st); err != nil {
		t.Fatalf("Defaults (first): %v", err)
	}
	before := make(map[string]store.Project, len(seed.DefaultProjects))
	for _, p := range seed.DefaultProjects {
		got, err := st.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetProject(%q): %v", p.ID, err)
		}
		before[p.ID] = got
	}

	if err := seed.Defaults(ctx, st); err != nil {
		t.Fatalf("Defaults (second): %v", err)
	}
	for _, p := range seed.DefaultProjects {
		got, err := st.GetProject(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetProject(%q) after reseed: %v", p.ID, err)
		}
		prev := before[p.ID]
		if !got.CreatedAt.Equal(prev.CreatedAt) {
			t.Errorf("project %q: created_at moved on reseed (%v → %v) — the row was recreated", p.ID, prev.CreatedAt, got.CreatedAt)
		}
		if !got.UpdatedAt.Equal(prev.UpdatedAt) {
			t.Errorf("project %q: updated_at moved on reseed (%v → %v) — a reseed is not an edit", p.ID, prev.UpdatedAt, got.UpdatedAt)
		}
		if got.Summary != p.Description {
			t.Errorf("project %q: summary changed on reseed: %q", p.ID, got.Summary)
		}
	}
}

// TestDefaultProjectsSeedNeverOverwritesADescription is the guard that matters
// most: the seeded description is a starting point, not a claim on the row. Once
// a person has written their own — or deliberately cleared the seeded one — a
// later `make seed` must leave it exactly as they left it.
func TestDefaultProjectsSeedNeverOverwritesADescription(t *testing.T) {
	st := projectSeedStore(t)
	ctx := context.Background()

	if err := seed.Defaults(ctx, st); err != nil {
		t.Fatalf("Defaults (first): %v", err)
	}
	if len(seed.DefaultProjects) == 0 {
		t.Fatal("DefaultProjects is empty")
	}
	target := seed.DefaultProjects[0]

	mine := "my own words about this project — do not touch"
	if _, err := st.UpdateProject(ctx, target.ID, store.ProjectPatch{Summary: &mine}); err != nil {
		t.Fatalf("UpdateProject(%q): %v", target.ID, err)
	}

	if err := seed.Defaults(ctx, st); err != nil {
		t.Fatalf("Defaults (second): %v", err)
	}

	got, err := st.GetProject(ctx, target.ID)
	if err != nil {
		t.Fatalf("GetProject(%q): %v", target.ID, err)
	}
	if got.Summary != mine {
		t.Errorf("project %q: reseed overwrote a user-edited description: got %q, want %q", target.ID, got.Summary, mine)
	}

	// The other seeded projects are unaffected by one row being edited.
	for _, p := range seed.DefaultProjects[1:] {
		other, err := st.GetProject(ctx, p.ID)
		if err != nil {
			t.Errorf("GetProject(%q): %v", p.ID, err)
			continue
		}
		if other.Summary != p.Description {
			t.Errorf("project %q: summary = %q, want the seeded description", p.ID, other.Summary)
		}
	}
}
