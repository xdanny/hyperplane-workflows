package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

func continuationSource(t *testing.T, st *Store) (Run, string) {
	t.Helper()
	id := "continuation-source-" + uniq()
	owner, err := st.OwnSDKRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	options := JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d,"request":{"workflow":"node-workflow","instruction":"Write then review","runtimeFingerprint":"abc","nodes":[{"id":"write","kind":"document","title":"Write","instruction":"Write"}]}}`, owner.SessionID()))
	if err = st.CreateRun(t.Context(), Run{RunID: id, DAG: "node-workflow", Project: "continuation-tests", State: "running", OwnerPID: os.Getpid(), Options: options, Doc: JSON(`{"recipe":{"name":"retained"}}`)}); err != nil {
		t.Fatal(err)
	}
	checkpoint := JSON(`{"phase":"running","threadId":"retained-thread","nodes":[{"id":"write","phase":"passed","document":{"path":"write.md","bytes":2,"sha256":"abc"}}],"usage":{"input_tokens":17}}`)
	if _, _, err = owner.SaveCheckpoint(t.Context(), id, "continuation-tests", os.Getpid(), checkpoint, "", nil); err != nil {
		t.Fatal(err)
	}
	if err = st.CancelRun(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	source, err := st.GetRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := SDKContinuationRevision(source)
	if err != nil {
		t.Fatal(err)
	}
	return source, revision
}

func continuationChild(t *testing.T, st *Store, source Run) (*SDKRunOwner, Run) {
	t.Helper()
	id := "continuation-child-" + uniq()
	owner, err := st.OwnSDKRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	options := JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d,"request":{"workflow":"node-workflow"},"continuation":{"sourceRunId":%q}}`, owner.SessionID(), source.RunID))
	return owner, Run{RunID: id, DAG: "node-workflow", Project: source.Project, State: "running", OwnerPID: os.Getpid(), Options: options}
}

func TestSDKContinuationRevisionIsStableAndBindsReviewedSource(t *testing.T) {
	base := Run{RunID: "source", State: "interrupted", Project: "one", Options: JSON(`{"b":2,"a":{"n":9007199254740993}}`), Doc: JSON(`{"sdk":{"phase":"interrupted","nodes":[]},"other":"unrelated"}`)}
	want, err := SDKContinuationRevision(base)
	if err != nil || len(want) != 64 {
		t.Fatalf("%s %v", want, err)
	}
	same := base
	same.Options = JSON(`{ "a": {"n":9007199254740993}, "b":2 }`)
	same.Doc = JSON(`{"other":"changed","continuation":{"runId":"child","reviewToken":"token"},"sdk":{"nodes":[],"phase":"interrupted"}}`)
	if got, err := SDKContinuationRevision(same); err != nil || got != want {
		t.Fatalf("canonicalization/claim changed review: %s %v", got, err)
	}
	for _, change := range []func(*Run){
		func(r *Run) { r.RunID = "different" }, func(r *Run) { r.State = "passed" }, func(r *Run) { r.Project = "other" }, func(r *Run) { r.Options = JSON(`{"a":{"n":9007199254740992},"b":2}`) }, func(r *Run) { r.Doc = JSON(`{"sdk":{"phase":"interrupted","nodes":[{"id":"new"}]}}`) },
	} {
		r := base
		change(&r)
		if got, err := SDKContinuationRevision(r); err != nil || got == want {
			t.Fatalf("source edit was not bound: %s %v", got, err)
		}
	}
}

func TestSDKContinuationPreservesSourceAndCommitsOneSuccessor(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source, token := continuationSource(t, st)
	eventsBefore, err := st.ListEvents(t.Context(), source.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	owner, child := continuationChild(t, st, source)
	release, err := owner.HoldContinuationSource(t.Context(), source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := st.DeleteRun(t.Context(), source.RunID); !errors.Is(err, ErrRunActive) {
		t.Fatalf("deleted leased source: %v", err)
	}
	initial := JSON(`{"phase":"running","threadId":"","continuation":{"sourceRunId":"retained"},"nodes":[]}`)
	event, err := owner.StartContinuation(t.Context(), source.RunID, token, child, initial)
	if err != nil || event.Seq != 1 || event.Type != "sdk.checkpoint" {
		t.Fatalf("%+v %v", event, err)
	}
	saved, err := st.GetRun(t.Context(), child.RunID)
	if err != nil || saved.ParentRun != source.RunID || saved.State != "running" {
		t.Fatalf("child: %+v %v", saved, err)
	}
	node, err := st.GetNode(t.Context(), child.RunID, "delivery")
	if err != nil || node.State != "running" || !bytes.Contains(node.Doc, []byte(`"nodes": []`)) {
		t.Fatalf("checkpoint missing: %+v %v", node, err)
	}
	after, err := st.GetRun(t.Context(), source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeDoc, afterDoc map[string]JSON
	_ = json.Unmarshal(source.Doc, &beforeDoc)
	_ = json.Unmarshal(after.Doc, &afterDoc)
	if after.State != source.State || !bytes.Equal(source.Summary, after.Summary) || !bytes.Equal(beforeDoc["sdk"], afterDoc["sdk"]) || !bytes.Equal(beforeDoc["recipe"], afterDoc["recipe"]) {
		t.Fatalf("source receipt overwritten: %s", after.Doc)
	}
	eventsAfter, err := st.ListEvents(t.Context(), source.RunID, 0, 100)
	if err != nil || !reflect.DeepEqual(eventsBefore, eventsAfter) {
		t.Fatalf("source journal changed: %v", err)
	}
	if got, err := SDKContinuationRevision(after); err != nil || got != token {
		t.Fatal("claim invalidated review token")
	}
	_, err = owner.StartContinuation(t.Context(), source.RunID, token, child, initial)
	var existing *SDKContinuationExists
	if !errors.As(err, &existing) || existing.RunID != child.RunID {
		t.Fatalf("retry: %v", err)
	}
	if _, err = owner.StartContinuation(t.Context(), source.RunID, strings.Repeat("0", 64), child, initial); !errors.Is(err, ErrContinuationChanged) {
		t.Fatal(err)
	}
	if err = st.CancelRun(t.Context(), child.RunID); err != nil {
		t.Fatal(err)
	}
	if state, _, err := owner.SaveCheckpoint(t.Context(), child.RunID, child.Project, child.OwnerPID, JSON(`{"phase":"passed"}`), "passed", nil); err != nil || state != "interrupted" {
		t.Fatalf("continuation bypassed cancellation: %s %v", state, err)
	}
	release()
	owner.Close()
	if err = st.DeleteRun(t.Context(), source.RunID); err != nil {
		t.Fatalf("released source cannot be deleted: %v", err)
	}
}

func TestSDKContinuationAdmissionRollsBackAndRequiresBothLocks(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source, token := continuationSource(t, st)
	owner, child := continuationChild(t, st, source)
	initial := JSON(`{"phase":"running","attempts":"invalid integer"}`)
	if _, err := owner.StartContinuation(t.Context(), source.RunID, token, child, initial); !errors.Is(err, ErrContinuationChanged) {
		t.Fatalf("no source lease: %v", err)
	}
	if release, err := owner.HoldContinuationSource(t.Context(), child.RunID); err == nil {
		release()
		t.Fatal("reentrant owner lease admitted")
	}
	release, err := owner.HoldContinuationSource(t.Context(), source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = owner.StartContinuation(t.Context(), source.RunID, token, child, initial); err == nil || errors.Is(err, ErrContinuationCommitUnknown) {
		t.Fatalf("precommit checkpoint failure incorrectly classified: %v", err)
	}
	if _, err = st.GetRun(t.Context(), child.RunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial child persisted: %v", err)
	}
	after, err := st.GetRun(t.Context(), source.RunID)
	if err != nil || !bytes.Equal(after.Doc, source.Doc) {
		t.Fatalf("partial claim persisted: %s %v", after.Doc, err)
	}
	// Session metadata cannot substitute for the actual child advisory lock.
	owner.mu.Lock()
	_, err = owner.conn.Exec(t.Context(), `SELECT pg_advisory_unlock(hashtextextended($1,684795135))`, child.RunID)
	owner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.StartContinuation(t.Context(), source.RunID, token, child, JSON(`{"phase":"running"}`)); !errors.Is(err, ErrContinuationChanged) {
		t.Fatalf("missing child lock accepted: %v", err)
	}
	owner.mu.Lock()
	_, err = owner.conn.Exec(t.Context(), `SELECT pg_advisory_lock(hashtextextended($1,684795135))`, child.RunID)
	if err == nil {
		_, err = owner.conn.Exec(t.Context(), `SELECT pg_advisory_unlock(hashtextextended($1,684795135))`, source.RunID)
	}
	owner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.StartContinuation(t.Context(), source.RunID, token, child, JSON(`{"phase":"running"}`)); !errors.Is(err, ErrContinuationChanged) {
		t.Fatalf("missing physical source lock accepted: %v", err)
	}
}

func TestSDKContinuationRejectsChangedMissingOrWrongScopeSources(t *testing.T) {
	for _, name := range []string{"changed checkpoint", "different project", "wrong session", "wrong child", "missing source", "cancelled caller", "cancelled owner"} {
		t.Run(name, func(t *testing.T) {
			st := testStore(t)
			defer st.Close()
			source, token := continuationSource(t, st)
			owner, child := continuationChild(t, st, source)
			sourceID := source.RunID
			if name == "missing source" {
				sourceID = "missing-" + uniq()
			}
			release, err := owner.HoldContinuationSource(t.Context(), sourceID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx := t.Context()
			switch name {
			case "changed checkpoint":
				_, err = st.Pool.Exec(ctx, `UPDATE runs SET doc=jsonb_set(doc,'{sdk,error}','"changed"') WHERE run_id=$1`, sourceID)
			case "different project":
				child.Project = "other"
			case "wrong session":
				child.Options = JSON(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":0,"request":{"workflow":"node-workflow"}}`)
			case "wrong child":
				child.RunID = "other-" + uniq()
			case "cancelled caller":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "cancelled owner":
				owner.cancel(context.Canceled)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = owner.StartContinuation(ctx, sourceID, token, child, JSON(`{"phase":"running"}`))
			want := ErrContinuationChanged
			if name == "missing source" {
				want = ErrNotFound
			}
			if name == "cancelled caller" || name == "cancelled owner" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("want %v got %v", want, err)
			}
		})
	}
}

func TestSDKContinuationConcurrentClaimsAndLiveSourceExclusion(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source, token := continuationSource(t, st)
	// Even a terminal row cannot be copied while its former process still owns it.
	blocker, err := st.OwnSDKRun(t.Context(), source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	first, childA := continuationChild(t, st, source)
	second, childB := continuationChild(t, st, source)
	if release, err := first.HoldContinuationSource(t.Context(), source.RunID); !errors.Is(err, ErrRunActive) {
		if release != nil {
			release()
		}
		t.Fatalf("copied live source: %v", err)
	}
	blocker.Close()
	type answer struct {
		run string
		err error
	}
	results := make(chan answer, 2)
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for _, entry := range []struct {
		o *SDKRunOwner
		r Run
	}{{first, childA}, {second, childB}} {
		wg.Add(1)
		go func(o *SDKRunOwner, r Run) {
			defer wg.Done()
			<-ready
			release, err := o.HoldContinuationSource(t.Context(), source.RunID)
			if err == nil {
				defer release()
				_, err = o.StartContinuation(t.Context(), source.RunID, token, r, JSON(`{"phase":"running"}`))
			}
			results <- answer{r.RunID, err}
		}(entry.o, entry.r)
	}
	close(ready)
	wg.Wait()
	close(results)
	wins := 0
	for result := range results {
		if result.err == nil {
			wins++
		} else {
			var exists *SDKContinuationExists
			if !errors.Is(result.err, ErrRunActive) && !errors.As(result.err, &exists) {
				t.Fatal(result.err)
			}
		}
	}
	if wins != 1 {
		t.Fatalf("started %d successors", wins)
	}
	var count int
	if err := st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM runs WHERE parent_run=$1`, source.RunID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("%d children %v", count, err)
	}
	first.Close()
	second.Close()
	if _, err := st.ReconcileOrphans(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Pool.Query(t.Context(), `SELECT state FROM runs WHERE parent_run=$1`, source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	states, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || !reflect.DeepEqual(states, []string{"interrupted"}) {
		t.Fatalf("orphan child: %v %v", states, err)
	}
}

func TestSDKContinuationOwnerCloseReleasesSourceLease(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source, _ := continuationSource(t, st)
	owner, _ := continuationChild(t, st, source)
	release, err := owner.HoldContinuationSource(t.Context(), source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := owner.HoldContinuationSource(t.Context(), source.RunID); err == nil {
		again()
		t.Fatal("source lease recursively acquired")
	}
	owner.Close()
	// Close acknowledges release even when the caller has not released the copy lease.
	if err := st.DeleteRun(t.Context(), source.RunID); err != nil {
		t.Fatalf("source lease leaked: %v", err)
	}
	release()
	release()
}

func TestSDKContinuationCommitFailureRequiresPreservingDestination(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source, token := continuationSource(t, st)
	owner, child := continuationChild(t, st, source)
	release, err := owner.HoldContinuationSource(t.Context(), source.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// A deferred constraint fails during Commit, after every admission statement
	// succeeds. The public error conservatively preserves the workspace even when
	// this injected failure happens to be a known server-side rollback.
	if _, err = st.Pool.Exec(t.Context(), `CREATE FUNCTION continuation_test_commit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected continuation commit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), `DROP FUNCTION continuation_test_commit_failure()`)
	if _, err = st.Pool.Exec(t.Context(), `CREATE CONSTRAINT TRIGGER continuation_test_commit_failure AFTER INSERT ON runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.run_id LIKE 'continuation-child-%') EXECUTE FUNCTION continuation_test_commit_failure()`); err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), `DROP TRIGGER continuation_test_commit_failure ON runs`)
	_, err = owner.StartContinuation(t.Context(), source.RunID, token, child, JSON(`{"phase":"running"}`))
	if !errors.Is(err, ErrContinuationCommitUnknown) || !strings.Contains(err.Error(), "injected continuation commit failure") {
		t.Fatalf("commit uncertainty/cause lost: %v", err)
	}
	if _, err = st.GetRun(t.Context(), child.RunID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("injected failure committed child: %v", err)
	}
	after, err := st.GetRun(t.Context(), source.RunID)
	if err != nil || !bytes.Equal(after.Doc, source.Doc) {
		t.Fatalf("injected failure committed claim: %s %v", after.Doc, err)
	}
}
