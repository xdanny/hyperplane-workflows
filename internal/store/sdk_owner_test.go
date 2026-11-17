package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSDKSessionOwnershipSurvivesReconciliationAndCancellationUntilReleased(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	id := "sdk-session-" + uniq()
	owner, err := st.OwnSDKRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	// A PID that is not alive on this host must not override database ownership.
	if err := st.CreateRun(t.Context(), Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: 0,
		Options: JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d}`, owner.SessionID()))}); err != nil {
		t.Fatal(err)
	}
	checkpoint := JSON(`{"phase":"approval_pending","threadId":"retained-thread","approval":{"id":"` + id + `-approval"},"usage":{"input_tokens":17}}`)
	if _, _, err := owner.SaveCheckpoint(t.Context(), id, "", 0, checkpoint, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SaveSDKCheckpoint(t.Context(), id, "", 0, checkpoint, "passed", nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("pooled connection bypassed session ownership: %v", err)
	}
	for _, cancelParent := range []bool{false, true} {
		if cancelParent {
			cancel()
			<-owner.Context.Done()
		}
		if second, err := st.OwnSDKRun(t.Context(), id); err == nil {
			second.Close()
			t.Fatal("second owner admitted")
		}
		if n, err := st.ReconcileOrphans(t.Context()); err != nil {
			t.Fatalf("reconciled active owner: %d %v", n, err)
		}
		run, err := st.GetRun(t.Context(), id)
		if err != nil || run.State != "running" {
			t.Fatalf("live run changed: %+v %v", run, err)
		}
	}
	owner.Close()
	// Reused/local live PIDs must not keep a disconnected session alive.
	if _, err := st.Pool.Exec(t.Context(), `UPDATE runs SET owner_pid=$2 WHERE run_id=$1`, id, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if n, err := st.ReconcileOrphans(t.Context()); err != nil || n < 1 {
		t.Fatalf("reconcile released owner: %d %v", n, err)
	}
	run, err := st.GetRun(t.Context(), id)
	if err != nil || run.State != "interrupted" || !bytes.Contains(run.Summary, []byte("retained-thread")) {
		t.Fatalf("lost checkpoint: %+v %v", run, err)
	}
	node, err := st.GetNode(t.Context(), id, "delivery")
	if err != nil || node.State != "interrupted" || !bytes.Contains(node.Doc, []byte("input_tokens")) {
		t.Fatalf("delivery differs: %+v %v", node, err)
	}
	var decision string
	if err := st.Pool.QueryRow(t.Context(), `SELECT state FROM records WHERE id=$1`, id+"-approval").Scan(&decision); err != nil || decision != "dismissed" {
		t.Fatalf("pending decision: %s %v", decision, err)
	}
	if _, err := st.DecideSDKApproval(t.Context(), id+"-approval", "approved"); !errors.Is(err, ErrSDKApprovalExpired) {
		t.Fatalf("abandoned approval accepted: %v", err)
	}
	events, err := st.ListEvents(t.Context(), id, 0, 100)
	if err != nil || len(events) != 2 || events[1].Type != "run.done" {
		t.Fatalf("missing terminal journal: %+v %v", events, err)
	}
	if n, err := st.ReconcileOrphans(t.Context()); err != nil {
		t.Fatalf("duplicate recovery: %d %v", n, err)
	}
	if again, _ := st.ListEvents(t.Context(), id, 0, 100); len(again) != 2 {
		t.Fatal("duplicated terminal event")
	}
}

func TestSDKOwnershipConnectionLossCancelsWork(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id := "sdk-disconnect-" + uniq()
	owner, err := st.OwnSDKRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := st.CreateRun(t.Context(), Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: os.Getpid(),
		Options: JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d}`, owner.SessionID()))}); err != nil {
		t.Fatal(err)
	}
	checkpoint := JSON(`{"phase":"running","threadId":"before-disconnect"}`)
	if _, _, err := owner.SaveCheckpoint(t.Context(), id, "", os.Getpid(), checkpoint, "", nil); err != nil {
		t.Fatal(err)
	}
	var stopped bool
	err = st.Pool.QueryRow(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype='advisory' AND granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
		AND classid::bigint=((hashtextextended($1,684795135) >> 32) & 4294967295)
		AND objid::bigint=(hashtextextended($1,684795135) & 4294967295) AND objsubid=1`, id).Scan(&stopped)
	if err != nil || !stopped {
		t.Fatalf("terminate own lock session: %v %v", stopped, err)
	}
	// Attempt completion immediately, without waiting for the monitor. The lost
	// session must fence the write and signal cancellation synchronously.
	if _, _, err := owner.SaveCheckpoint(t.Context(), id, "", os.Getpid(), JSON(`{"phase":"passed","passed":true}`), "passed", nil); err == nil {
		t.Fatal("disconnected owner published success")
	}
	if owner.Context.Err() == nil {
		t.Fatal("checkpoint failure did not cancel lost owner")
	}
	select {
	case <-owner.Context.Done():
		if !strings.Contains(context.Cause(owner.Context).Error(), "ownership connection lost") {
			t.Fatal(context.Cause(owner.Context))
		}
	case <-time.After(4 * time.Second):
		t.Fatal("lost database ownership did not cancel work")
	}
	if n, err := st.ReconcileOrphans(t.Context()); err != nil || n < 1 {
		t.Fatalf("recover disconnected owner: %d %v", n, err)
	}
	run, err := st.GetRun(t.Context(), id)
	if err != nil || run.State != "interrupted" || !bytes.Contains(run.Doc, []byte("before-disconnect")) {
		t.Fatalf("recovered evidence: %+v %v", run, err)
	}
	if _, _, err := st.SaveSDKCheckpoint(t.Context(), id, "", os.Getpid(), JSON(`{"phase":"passed"}`), "passed", nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("late pooled completion accepted: %v", err)
	}
	events, err := st.ListEvents(t.Context(), id, 0, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("late final changed journal: %+v %v", events, err)
	}
}

func TestSDKReconciliationIsAtomicAndConcurrent(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "sdk-reconcile-" + uniq()
	owner, err := st.OwnSDKRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: os.Getpid(),
		Options: JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d}`, owner.SessionID()))}); err != nil {
		t.Fatal(err)
	}
	checkpoint := JSON(`{"phase":"approval_pending","approval":{"id":"` + id + `-approval"}}`)
	if _, _, err := owner.SaveCheckpoint(ctx, id, "", os.Getpid(), checkpoint, "", nil); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	// Force a terminal-journal failure after the run and delivery UPDATEs. The
	// interrupted state must not leak from a transaction that cannot record it.
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE events ADD CONSTRAINT recovery_test_no_terminal CHECK(type <> 'run.done') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), `ALTER TABLE events DROP CONSTRAINT IF EXISTS recovery_test_no_terminal`)
	if _, err := st.reconcileRun(ctx, id, true); err == nil {
		t.Fatal("journal failure was ignored")
	}
	run, _ := st.GetRun(ctx, id)
	node, _ := st.GetNode(ctx, id, "delivery")
	var decision string
	err = st.Pool.QueryRow(ctx, `SELECT state FROM records WHERE id=$1`, id+"-approval").Scan(&decision)
	if err != nil || run.State != "running" || node.State != "running" || decision != "pending" {
		t.Fatalf("partial recovery: %s %s %s %v", run.State, node.State, decision, err)
	}
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE events DROP CONSTRAINT recovery_test_no_terminal`); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		changed bool
		err     error
	}
	done := make(chan outcome, 2)
	for range 2 {
		go func() { changed, err := st.reconcileRun(ctx, id, true); done <- outcome{changed, err} }()
	}
	count := 0
	for range 2 {
		result := <-done
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.changed {
			count++
		}
	}
	events, err := st.ListEvents(ctx, id, 0, 100)
	if err != nil || count != 1 || len(events) != 2 || events[1].Type != "run.done" {
		t.Fatalf("concurrent recovery: %d %+v %v", count, events, err)
	}
}
