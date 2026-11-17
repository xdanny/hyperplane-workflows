package compose_test

import (
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/compose"
)

// TestStarterBriefsParse verifies that the embedded briefs.json parses
// correctly and contains the four new M27 domain workflows.
func TestStarterBriefsParse(t *testing.T) {
	briefs, err := compose.StarterBriefs()
	if err != nil {
		t.Fatalf("StarterBriefs(): %v", err)
	}
	if len(briefs) == 0 {
		t.Fatal("StarterBriefs(): returned empty list")
	}

	wantIDs := []string{"trip-planning", "deep-research", "interview-prep", "finance-review", "code-review"}
	got := make(map[string]compose.StarterBrief, len(briefs))
	for _, b := range briefs {
		got[b.ID] = b
	}

	for _, id := range wantIDs {
		b, ok := got[id]
		if !ok {
			t.Errorf("StarterBriefs(): missing brief %q", id)
			continue
		}
		if b.Title == "" {
			t.Errorf("brief %q: title is empty", id)
		}
		if b.Brief == "" {
			t.Errorf("brief %q: brief text is empty", id)
		}
		if b.Category == "" {
			t.Errorf("brief %q: category is empty", id)
		}
		if b.Recipe != id {
			t.Errorf("brief %q must open its editable SDK template, got %+v", id, b)
		}
	}
}

// TestStarterBriefsNoDuplicateIDs verifies no duplicate IDs exist in the brief list.
func TestStarterBriefsNoDuplicateIDs(t *testing.T) {
	briefs, err := compose.StarterBriefs()
	if err != nil {
		t.Fatalf("StarterBriefs(): %v", err)
	}
	seen := make(map[string]bool, len(briefs))
	for _, b := range briefs {
		if b.ID == "" {
			t.Error("StarterBriefs(): a brief has an empty id")
			continue
		}
		if seen[b.ID] {
			t.Errorf("StarterBriefs(): duplicate brief id %q", b.ID)
		}
		seen[b.ID] = true
	}
}
