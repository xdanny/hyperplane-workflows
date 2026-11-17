package store_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("no postgres reachable (%v); skipping store tests", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestEvalsetCRUD(t *testing.T) {
	st := openTestStore(t)
	defer st.Close()

	ctx := context.Background()

	es := store.Evalset{
		ID:          "test-evalset-crud",
		Name:        "Test Evalset",
		Description: "for testing",
		SkillRef:    "test-skill",
		Cases: []store.EvalCase{
			{ID: "c1", Input: json.RawMessage(`{"x":1}`), Criteria: "must be nonempty"},
		},
	}

	if err := st.UpsertEvalset(ctx, es); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := st.GetEvalset(ctx, es.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != es.Name {
		t.Errorf("name mismatch: got %q want %q", got.Name, es.Name)
	}
	if len(got.Cases) != 1 {
		t.Errorf("expected 1 case, got %d", len(got.Cases))
	}

	list, err := st.ListEvalsets(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, e := range list {
		if e.ID == es.ID {
			found = true
		}
	}
	if !found {
		t.Error("evalset not found in list")
	}

	run := store.EvalRun{
		ID:        "test-run-crud-" + time.Now().Format("150405.000000000"),
		EvalsetID: es.ID,
		Target:    "test-target",
		Engine:    "mock",
		Status:    "running",
		Results:   []store.EvalCaseResult{},
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM eval_runs WHERE id=$1`, run.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM evalsets WHERE id=$1`, es.ID)
	})
	if err := st.CreateEvalRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	run.Status = "done"
	run.PassRate = 1.0
	run.MeanScore = 1.0
	if err := st.UpdateEvalRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	gotRun, err := st.GetEvalRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != "done" {
		t.Errorf("expected status=done, got %s", gotRun.Status)
	}

	runs, err := st.ListEvalRuns(ctx, es.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) == 0 {
		t.Error("expected at least 1 run")
	}
}
