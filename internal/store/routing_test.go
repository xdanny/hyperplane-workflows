package store

import (
	"context"
	"testing"
	"time"
)

func TestRoutingPolicyCRUD(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	ctx := context.Background()
	scope := "skill:routing-test-" + time.Now().Format("150405.000000")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM routing_policy WHERE scope=$1`, scope)
	})

	conf := 0.92
	margin := 0.1
	p := RoutingPolicy{
		Scope:      scope,
		Engine:     "codex-local",
		Confidence: &conf,
		Margin:     &margin,
		EvalRunA:   "er-local",
		EvalRunB:   "er-codex",
		Note:       "local within margin",
	}

	// Upsert and round-trip.
	if err := st.UpsertRoutingPolicy(ctx, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := st.GetRoutingPolicy(ctx, scope)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Engine != "codex-local" {
		t.Errorf("engine: got %q want %q", got.Engine, "codex-local")
	}
	if got.EvalRunA != "er-local" {
		t.Errorf("eval_run_a: got %q want %q", got.EvalRunA, "er-local")
	}
	if got.Confidence == nil || *got.Confidence != conf {
		t.Errorf("confidence: got %v want %v", got.Confidence, conf)
	}

	// List must include it.
	list, err := st.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, rp := range list {
		if rp.Scope == scope {
			found = true
		}
	}
	if !found {
		t.Error("routing policy not found in list")
	}

	// Update (upsert again changes engine).
	p.Engine = "codex"
	p.Note = "local lost"
	if err := st.UpsertRoutingPolicy(ctx, p); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got2, err := st.GetRoutingPolicy(ctx, scope)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got2.Engine != "codex" {
		t.Errorf("engine after update: got %q want %q", got2.Engine, "codex")
	}

	// Delete.
	if err := st.DeleteRoutingPolicy(ctx, scope); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = st.GetRoutingPolicy(ctx, scope)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Delete non-existent → ErrNotFound.
	if err := st.DeleteRoutingPolicy(ctx, scope); err != ErrNotFound {
		t.Errorf("double-delete: want ErrNotFound, got %v", err)
	}
}

func TestGetRoutingPolicyNotFound(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	ctx := context.Background()
	_, err := st.GetRoutingPolicy(ctx, "skill:does-not-exist-xyz")
	if err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
