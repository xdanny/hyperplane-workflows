package store

import (
	"context"
	"testing"
)

// TestListProjectsLabelsUnscopedBucket pins the shape of the empty-tag row.
// It is an aggregate, not a project: it must arrive NAMED (so nothing renders
// it as a blank card) but still unregistered (so every picker can exclude it
// with one honest check).
func TestListProjectsLabelsUnscopedBucket(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	// Guarantee the bucket is non-empty: an untagged run puts a row in it.
	runID := "unscoped-run-" + uniq()
	if err := st.CreateRun(ctx, Run{RunID: runID, DAG: "unscoped-shape", State: "passed"}); err != nil {
		t.Fatalf("create untagged run: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM runs WHERE run_id=$1`, runID) })

	list, err := ListProjectsOrFail(t, st)
	if err != nil {
		t.Fatal(err)
	}
	pc, ok := findProject(list, UnscopedProject)
	if !ok {
		t.Fatalf("the unscoped bucket is missing from ListProjects: %+v", list)
	}
	if pc.Name != UnscopedProjectName {
		t.Fatalf("unscoped bucket name = %q, want %q — a nameless row renders as a blank card", pc.Name, UnscopedProjectName)
	}
	if pc.Registered {
		t.Fatalf("the unscoped bucket must never claim to be a registered project: %+v", pc)
	}
	if pc.Runs < 1 {
		t.Fatalf("unscoped bucket did not count the untagged run: %+v", pc)
	}
}

// ListProjectsOrFail is the shared read used by the project-scope tests.
func ListProjectsOrFail(t *testing.T, st *Store) ([]ProjectCount, error) {
	t.Helper()
	return st.ListProjects(context.Background(), true)
}
