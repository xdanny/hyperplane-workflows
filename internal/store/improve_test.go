package store

import (
	"encoding/json"
	"testing"
)

// TestImproveRunRoundTrip verifies Create → Get → Update → List against a real
// Postgres instance. The test skips when DATABASE_URL is not reachable so that
// `go test ./...` stays green on machines without a local database.
func TestImproveRunRoundTrip(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()

	id := "improve-test-" + uniq()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM improve_runs WHERE id=$1`, id)
	})

	run := ImproveRun{
		ID:               id,
		SkillsConsidered: 5,
		SkillsOptimized:  3,
		SkillsPromoted:   1,
		Items:            json.RawMessage(`[{"skillRef":"s1","status":"promoted"}]`),
		Status:           "running",
	}

	// Create.
	gotID, err := st.CreateImproveRun(ctx, run)
	if err != nil {
		t.Fatalf("CreateImproveRun: %v", err)
	}
	if gotID != id {
		t.Errorf("expected id %q, got %q", id, gotID)
	}

	// Get.
	rec, err := st.GetImproveRun(ctx, id)
	if err != nil {
		t.Fatalf("GetImproveRun: %v", err)
	}
	if rec.SkillsConsidered != 5 || rec.SkillsPromoted != 1 || rec.Status != "running" {
		t.Errorf("unexpected record: %+v", rec)
	}

	// Update.
	run.Status = "done"
	run.SkillsOptimized = 4
	if err := st.UpdateImproveRun(ctx, run); err != nil {
		t.Fatalf("UpdateImproveRun: %v", err)
	}

	// Verify update.
	rec2, err := st.GetImproveRun(ctx, id)
	if err != nil {
		t.Fatalf("GetImproveRun after update: %v", err)
	}
	if rec2.Status != "done" || rec2.SkillsOptimized != 4 {
		t.Errorf("update not reflected: %+v", rec2)
	}

	// List — record must appear.
	list, err := st.ListImproveRuns(ctx)
	if err != nil {
		t.Fatalf("ListImproveRuns: %v", err)
	}
	var found bool
	for _, r := range list {
		if r.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created improve run not found in list")
	}

	// GetImproveRun for missing id returns ErrNotFound.
	_, err = st.GetImproveRun(ctx, "does-not-exist")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for missing id, got %v", err)
	}
}
