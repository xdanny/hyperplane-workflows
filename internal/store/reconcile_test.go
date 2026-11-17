package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestReconcileOrphans verifies the PID-liveness-aware reconciliation logic:
//   - A run owned by a dead PID is marked interrupted.
//   - A run owned by the current (alive) process is left running.
func TestReconcileOrphans(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	// Spawn a short-lived process and wait for it to exit, so we have a
	// guaranteed-dead PID.  On Darwin/Linux os.StartProcess returns quickly
	// and the child immediately exits, recycling the pid only much later.
	// We use a simple true(1) shell built-in via /usr/bin/true.
	proc, err := os.StartProcess("/usr/bin/true", []string{"true"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot start process to get a dead PID: %v", err)
	}
	_, _ = proc.Wait() // reap so PID is marked dead; OS won't recycle it quickly
	deadPID := proc.Pid

	// Create two runs: one orphaned (dead pid), one owned by this process (alive).
	orphanID := "test-orphan-" + uniq()
	liveID := "test-live-" + uniq()
	t.Cleanup(func() {
		_ = st.DeleteRun(ctx, orphanID)
		_ = st.DeleteRun(ctx, liveID)
	})

	if err := st.CreateRun(ctx, Run{
		RunID: orphanID, DAG: "hello", State: "running",
		StartedAt: time.Now(), OwnerPID: deadPID,
	}); err != nil {
		t.Fatalf("create orphan run: %v", err)
	}
	if err := st.CreateRun(ctx, Run{
		RunID: liveID, DAG: "hello", State: "running",
		StartedAt: time.Now(), OwnerPID: os.Getpid(),
	}); err != nil {
		t.Fatalf("create live run: %v", err)
	}

	n, err := st.ReconcileOrphans(ctx)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}
	// At least the orphan run should have been reconciled. (The shared dev DB may
	// have other orphaned runs from previous test runs; n >= 1 is the assertion.)
	if n < 1 {
		t.Fatalf("expected at least 1 run to be reconciled, got %d", n)
	}

	// Orphan run must now be interrupted.
	orphan, err := st.GetRun(ctx, orphanID)
	if err != nil {
		t.Fatalf("get orphan run: %v", err)
	}
	if orphan.State != "interrupted" {
		t.Errorf("orphan run: want state=interrupted, got %q", orphan.State)
	}

	// Live run must remain running.
	live, err := st.GetRun(ctx, liveID)
	if err != nil {
		t.Fatalf("get live run: %v", err)
	}
	if live.State != "running" {
		t.Errorf("live run: want state=running, got %q", live.State)
	}

	// Clean up: mark the live run terminal so the shared dev DB isn't polluted.
	_ = st.SetRunState(ctx, liveID, "interrupted")
}

// TestCancelRun verifies the CancelRun store method.
func TestCancelRun(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	runID := "test-cancel-" + uniq()
	t.Cleanup(func() { _ = st.DeleteRun(ctx, runID) })

	if err := st.CreateRun(ctx, Run{
		RunID: runID, DAG: "hello", State: "running",
		StartedAt: time.Now(), OwnerPID: os.Getpid(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := st.CancelRun(ctx, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	r, err := st.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run after cancel: %v", err)
	}
	if r.State != "interrupted" {
		t.Errorf("want state=interrupted, got %q", r.State)
	}
	if r.EndedAt.IsZero() {
		t.Error("want ended_at set after cancel, got zero")
	}

	// Cancelling an already-terminal run must return ErrNotRunning.
	err = st.CancelRun(ctx, runID)
	if err != ErrNotRunning {
		t.Errorf("second cancel: want ErrNotRunning, got %v", err)
	}

	// Cancelling a non-existent run must return ErrNotFound.
	err = st.CancelRun(ctx, "does-not-exist-"+uniq())
	if err != ErrNotFound {
		t.Errorf("missing run cancel: want ErrNotFound, got %v", err)
	}
}
