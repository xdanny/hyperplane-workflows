package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

func TestSDKCheckpointIsAtomicScopedAndCannotUndoCancellation(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()
	id := "sdk-" + uniq()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "app-ideator", Project: "sdk-tests", State: "running", OwnerPID: os.Getpid(), Options: JSON(`{"runtime":"codex-sdk"}`)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(ctx, id) })
	checkpoint := JSON(`{"phase":"running","threadId":"owned-session","attempt":0}`)
	if _, _, err := st.SaveSDKCheckpoint(ctx, id, "sdk-tests", os.Getpid(), checkpoint, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct {
		project string
		owner   int
	}{{"another-project", os.Getpid()}, {"sdk-tests", -1}} {
		if _, _, err := st.SaveSDKCheckpoint(ctx, id, scope.project, scope.owner, checkpoint, "", nil); !errors.Is(err, ErrNotRunning) {
			t.Fatalf("scope accepted: %v", err)
		}
	}
	// Node persistence fails after the run UPDATE. The entire checkpoint must roll back.
	if _, _, err := st.SaveSDKCheckpoint(ctx, id, "sdk-tests", os.Getpid(), JSON(`{"phase":"correction_pending","attempt":"invalid"}`), "", nil); err == nil {
		t.Fatal("invalid checkpoint passed")
	}
	run, _ := st.GetRun(ctx, id)
	if !bytes.Contains(run.Doc, []byte("owned-session")) || bytes.Contains(run.Doc, []byte("correction_pending")) {
		t.Fatalf("failed node write changed the run: %s", run.Doc)
	}
	events, err := st.ListEvents(ctx, id, 0, 100)
	if err != nil || len(events) != 1 {
		t.Fatalf("partially persisted checkpoint: %+v %v", events, err)
	}
	if err := st.CancelRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SaveSDKCheckpoint(ctx, id, "sdk-tests", os.Getpid(), checkpoint, "", nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("cancelled run accepted correction: %v", err)
	}
	state, event, err := st.SaveSDKCheckpoint(ctx, id, "sdk-tests", os.Getpid(), checkpoint, "passed", JSON(`{"ok":true}`))
	if err != nil || state != "interrupted" {
		t.Fatalf("completion undid cancellation: %s %v", state, err)
	}
	if !bytes.Contains(event.Data, []byte(`"passed": false`)) || !bytes.Contains(event.Data, []byte(`"phase": "interrupted"`)) {
		t.Fatalf("event contradicted committed cancellation: %s", event.Data)
	}
	node, _ := st.GetNode(ctx, id, "delivery")
	if node.State != "interrupted" || string(node.Output) != "{}" {
		t.Fatalf("cancelled delivery published: %+v", node)
	}
}

func TestSDKOrphanKeepsIdentityAndInterruptsDelivery(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()
	id := "sdk-orphan-" + uniq()
	if err := st.CreateRun(ctx, Run{RunID: id, DAG: "app-ideator", State: "running", OwnerPID: 0, Options: JSON(`{"runtime":"codex-sdk"}`)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRun(ctx, id) })
	if _, _, err := st.SaveSDKCheckpoint(ctx, id, "", 0, JSON(`{"phase":"running","threadId":"orphan-session","attempt":1,"usage":{"input_tokens":17}}`), "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReconcileOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := st.GetRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.GetNode(ctx, id, "delivery")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "interrupted" || node.State != "interrupted" || !bytes.Contains(node.Doc, []byte("orphan-session")) || !bytes.Contains(node.Doc, []byte("input_tokens")) {
		t.Fatalf("lost interrupted SDK state: run=%+v node=%+v", run, node)
	}
}
