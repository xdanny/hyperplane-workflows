package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestSDKCancelIsAtomicBeforeOwnerCrash(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "sdk-cancel-atomic-" + uniq()
	owner, err := st.OwnSDKRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: os.Getpid(), Options: JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d}`, owner.SessionID()))}); err != nil {
		t.Fatal(err)
	}
	checkpoint := JSON(`{"phase":"approval_pending","threadId":"retained-thread","usage":{"input_tokens":17},"approval":{"id":"` + id + `-approval"}}`)
	if _, _, err := owner.SaveCheckpoint(ctx, id, "", os.Getpid(), checkpoint, "", nil); err != nil {
		t.Fatal(err)
	}
	// A failure after the run/delivery writes must roll back every cancellation effect.
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE events ADD CONSTRAINT cancel_test_no_terminal CHECK(type <> 'run.done') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), `ALTER TABLE events DROP CONSTRAINT IF EXISTS cancel_test_no_terminal`)
	if err := st.CancelRun(ctx, id); err == nil {
		t.Fatal("journal failure ignored")
	}
	run, _ := st.GetRun(ctx, id)
	node, _ := st.GetNode(ctx, id, "delivery")
	var decision string
	if err := st.Pool.QueryRow(ctx, `SELECT state FROM records WHERE id=$1`, id+"-approval").Scan(&decision); err != nil {
		t.Fatal(err)
	}
	if run.State != "running" || node.State != "running" || decision != "pending" {
		t.Fatalf("partial cancellation: %s %s %s", run.State, node.State, decision)
	}
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE events DROP CONSTRAINT cancel_test_no_terminal`); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	// No terminal checkpoint is ever written by the worker: simulate its crash now.
	owner.Close()
	run, _ = st.GetRun(ctx, id)
	node, _ = st.GetNode(ctx, id, "delivery")
	if run.State != "interrupted" || node.State != "interrupted" || !bytes.Contains(run.Summary, []byte("retained-thread")) || !bytes.Contains(node.Doc, []byte("input_tokens")) || node.Error != "Execution cancelled; work was not replayed" {
		t.Fatalf("incomplete cancellation: %+v %+v", run, node)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT state FROM records WHERE id=$1`, id+"-approval").Scan(&decision); err != nil || decision != "dismissed" {
		t.Fatalf("approval: %s %v", decision, err)
	}
	events, err := st.ListEvents(ctx, id, 0, 100)
	if err != nil || len(events) != 2 || events[1].Type != "run.done" {
		t.Fatalf("journal: %+v %v", events, err)
	}
	if err := st.CancelRun(ctx, id); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("repeat cancel: %v", err)
	}
	if _, err := st.ReconcileOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := st.ListEvents(ctx, id, 0, 100)
	if len(after) != 2 {
		t.Fatal("duplicate terminal journal")
	}
}

func TestDeleteRunRequiresStoppedAndReleasedOwner(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "sdk-delete-" + uniq()
	owner, err := st.OwnSDKRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: os.Getpid(), Options: JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d}`, owner.SessionID()))}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteRun(ctx, id); !errors.Is(err, ErrRunActive) {
		t.Fatalf("deleted active owner: %v", err)
	}
	if err := st.CancelRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteRun(ctx, id); !errors.Is(err, ErrRunActive) {
		t.Fatalf("deleted cancelled but live owner: %v", err)
	}
	owner.Close()
	if err := st.DeleteRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRun(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not deleted: %v", err)
	}
	if err := st.DeleteRun(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
	// Running records without a session cannot bypass the guard either.
	id = "running-delete-" + uniq()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", State: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteRun(ctx, id); !errors.Is(err, ErrRunActive) {
		t.Fatalf("deleted running record: %v", err)
	}
	if err := st.CancelRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteRun(ctx, id); err != nil {
		t.Fatal(err)
	}
}
