package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestApprovalConsumptionWaitsForCancellationAndCannotConsumeStaleDecision(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "sdk-approval-race-" + uniq()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: os.Getpid(), Options: JSON(`{"runtime":"codex-sdk"}`)}); err != nil {
		t.Fatal(err)
	}
	approvalID := id + "-approval"
	if _, _, err := st.SaveSDKCheckpoint(ctx, id, "", os.Getpid(), JSON(`{"phase":"approval_pending","approval":{"id":"`+approvalID+`"}}`), "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DecideSDKApproval(ctx, approvalID, "approved"); err != nil {
		t.Fatal(err)
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var blocker int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if changed, err := interruptRun(ctx, tx, id, "Execution cancelled; work was not replayed"); err != nil || !changed {
		t.Fatalf("stage cancellation: %v %v", changed, err)
	}
	done := make(chan error, 1)
	go func() { done <- st.ConsumeSDKApproval(ctx, id, approvalID) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("consumption bypassed cancelling transaction: %v", err)
		default:
		}
		var blocked bool
		if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("consumption did not wait on the run row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrSDKApprovalExpired) {
			t.Fatalf("consumed cancelled decision: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consumption did not finish")
	}
	record, err := st.GetRecord(ctx, "approvals", approvalID)
	if err != nil || record.Consumed {
		t.Fatalf("cancelled approval consumed: %+v %v", record, err)
	}
}
