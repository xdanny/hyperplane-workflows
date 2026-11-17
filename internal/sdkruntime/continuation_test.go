package sdkruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func waitContinuation(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("continuation condition was not reached")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Stop a real SDK worker after it saved one document and entered the next turn.
// Only the model executable is a fixture; SDK transport, files and PostgreSQL are real.
func interruptedWorkflow(t *testing.T, st *store.Store, approval bool, configure ...func(*Options)) (Options, string, store.Run) {
	t.Helper()
	fixture(t, "nodes", "hang")
	if approval {
		t.Setenv("SDK_FIXTURE_APPROVAL", "1")
	}
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SDK_FIXTURE_CALLS", calls)
	opts := Options{RunID: NewRunID("continuation-source"), RunsDir: t.TempDir(), Project: "continuation-runtime-test", Goal: "Compare options", TokenBudget: 12345}
	for _, change := range configure {
		change(&opts)
	}
	nodes := []sdkworkflow.Node{{ID: "explore", Title: "Explore", Instruction: "Find options"}}
	if approval {
		nodes = append(nodes, sdkworkflow.Node{ID: "approval", Kind: "approval", Title: "Approve options", Instruction: "Review these options"})
	}
	nodes = append(nodes, sdkworkflow.Node{ID: "review", Title: "Review", Instruction: "Compare options"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := RunNodeWorkflow(ctx, st, opts, "Compare options", nodes)
		if err == nil && result.State != "interrupted" {
			err = errors.New("source did not stop")
		}
		done <- err
	}()
	if approval {
		id := opts.RunID + ":approval:approval"
		waitContinuation(t, func() bool { _, err := st.GetRecord(t.Context(), "approvals", id); return err == nil })
		if _, err := st.DecideSDKApproval(t.Context(), id, "approved"); err != nil {
			t.Fatal(err)
		}
	}
	waitContinuation(t, func() bool { data, _ := os.ReadFile(calls); return string(data) == "explore\nreview\n" })
	// The fixture's call ledger precedes its SDK thread.started event. Wait for
	// the backend checkpoint before cancelling: only committed attempts survive
	// interruption, and this scenario deliberately stops during attempt two.
	waitContinuation(t, func() bool {
		run, err := st.GetRun(t.Context(), opts.RunID)
		if err != nil {
			return false
		}
		var doc struct {
			SDK struct {
				Attempts int
				Nodes    []struct{ ID, Phase string }
			}
		}
		if json.Unmarshal(run.Doc, &doc) != nil || run.State != "running" || doc.SDK.Attempts != 2 {
			return false
		}
		for _, node := range doc.SDK.Nodes {
			if node.ID == "review" {
				return node.Phase == "running"
			}
		}
		return false
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("source did not terminate")
	}
	run, err := st.GetRun(t.Context(), opts.RunID)
	if err != nil || run.State != "interrupted" {
		t.Fatalf("source: %+v %v", run, err)
	}
	return opts, calls, run
}

func sdkCheckpoint(t *testing.T, run store.Run) json.RawMessage {
	t.Helper()
	var doc struct {
		SDK json.RawMessage `json:"sdk"`
	}
	if err := json.Unmarshal(run.Doc, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.SDK
}

func TestContinuationReusesFilesAndPreservesStoppedReceipt(t *testing.T) {
	// Keep the former race deterministic: the model process becomes observable
	// before it emits the event that publishes the second attempt checkpoint.
	t.Setenv("SDK_FIXTURE_DELAY_STARTED", "1")
	st := runtimeStore(t)
	opts, calls, source := interruptedWorkflow(t, st, false)
	var sourceAccounting codex.WorkflowResult
	if err := json.Unmarshal(sdkCheckpoint(t, source), &sourceAccounting); err != nil {
		t.Fatal(err)
	}
	if sourceAccounting.Attempts != 2 || sourceAccounting.Usage.Input != 17 || sourceAccounting.Usage.Output != 2 {
		t.Fatalf("interruption lost its committed attempt: %s", source.Doc)
	}
	events, err := st.ListEvents(t.Context(), source.RunID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(opts.RunsDir, source.RunID, "nodes/delivery/OUTPUTS")
	original, err := os.ReadFile(filepath.Join(workspace, "explore.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "review.md"), []byte("uncommitted partial output"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := ReviewContinuation(t.Context(), st, opts.RunsDir, source.RunID)
	if err != nil || !review.Eligible || len(review.Reused) != 1 || review.Restart == nil || review.Restart.ID != "review" {
		t.Fatalf("review: %+v %v", review, err)
	}
	entries, _ := os.ReadDir(opts.RunsDir)
	if len(entries) != 1 {
		t.Fatalf("review created workspaces: %v", entries)
	}
	data, _ := os.ReadFile(calls)
	if string(data) != "explore\nreview\n" {
		t.Fatalf("review ran model: %s", data)
	}
	// A changed pin is rejected before creating a successor; restoring the file
	// makes the same recorded definition reviewable again.
	if err := os.WriteFile(filepath.Join(workspace, "explore.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked, err := ReviewContinuation(t.Context(), st, opts.RunsDir, source.RunID)
	if err != nil || blocked.Eligible || blocked.Reason == "" {
		t.Fatalf("changed pin: %+v %v", blocked, err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "explore.md"), original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	t.Setenv("SDK_FIXTURE_CONTINUATION", "1")
	t.Setenv("HYPERPLANE_MODEL", "must-not-replace-reviewed-model")
	child := Options{RunID: NewRunID("continued"), RunsDir: opts.RunsDir, TokenBudget: 999999, AutoRepair: true}
	result, err := RunContinuation(t.Context(), st, child, source.RunID, review.ReviewToken)
	if err != nil || result.State != "passed" {
		t.Fatalf("continue: %+v %v", result, err)
	}
	continued, err := st.GetRun(t.Context(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var launch struct {
		Request      codex.WorkflowRequest `json:"request"`
		Continuation struct {
			SourceRunID       string `json:"sourceRunId"`
			UsageIsLowerBound bool   `json:"usageIsLowerBound"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal(continued.Options, &launch); err != nil {
		t.Fatal(err)
	}
	if continued.ParentRun != source.RunID || launch.Request.MaxTokens != 12345 || launch.Request.Model != "fixture-model" || launch.Request.AutoRepair || launch.Continuation.SourceRunID != source.RunID || !launch.Continuation.UsageIsLowerBound {
		t.Fatalf("lost reviewed settings: %s", continued.Options)
	}
	var checkpoint codex.WorkflowResult
	if err := json.Unmarshal(sdkCheckpoint(t, continued), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Usage.Input != 34 || checkpoint.Usage.Cached != 6 || checkpoint.Usage.Output != 4 || checkpoint.Attempts != 3 || len(checkpoint.DefinitionFingerprint) != 64 || checkpoint.ReuseThrough != 1 {
		t.Fatalf("lost continuation accounting: %s", continued.Doc)
	}
	reused, err := os.ReadFile(filepath.Join(launch.Request.Workspace, "explore.md"))
	if err != nil || !bytes.Equal(reused, original) {
		t.Fatalf("reused output: %s %v", reused, err)
	}
	partial, err := os.ReadFile(filepath.Join(workspace, "review.md"))
	if err != nil || string(partial) != "uncommitted partial output" {
		t.Fatalf("source partial changed: %s %v", partial, err)
	}
	data, _ = os.ReadFile(calls)
	if string(data) != "explore\nreview\nreview\n" {
		t.Fatalf("completed step reran: %s", data)
	}
	after, err := st.GetRun(t.Context(), source.RunID)
	if err != nil || !bytes.Equal(source.Summary, after.Summary) || !bytes.Equal(sdkCheckpoint(t, source), sdkCheckpoint(t, after)) || after.State != "interrupted" {
		t.Fatalf("source receipt changed: %s %v", after.Doc, err)
	}
	afterEvents, err := st.ListEvents(t.Context(), source.RunID, 0, 1000)
	if err != nil || len(afterEvents) != len(events) {
		t.Fatalf("source journal changed: %d => %d %v", len(events), len(afterEvents), err)
	}
	_, err = RunContinuation(t.Context(), st, Options{RunID: NewRunID("duplicate"), RunsDir: opts.RunsDir}, source.RunID, review.ReviewToken)
	var exists *store.SDKContinuationExists
	if !errors.As(err, &exists) || exists.RunID != result.RunID {
		t.Fatalf("idempotency: %v", err)
	}
	_, err = RunContinuation(t.Context(), st, Options{RunsDir: opts.RunsDir}, source.RunID, "stale")
	if !errors.Is(err, store.ErrContinuationChanged) {
		t.Fatalf("stale accepted: %v", err)
	}
	entries, _ = os.ReadDir(opts.RunsDir)
	if len(entries) != 2 {
		t.Fatalf("duplicate created workspace: %v", entries)
	}
	claimed, err := ReviewContinuation(t.Context(), st, opts.RunsDir, source.RunID)
	if err != nil || claimed.Eligible || claimed.ContinuedRunID != result.RunID {
		t.Fatalf("claim discovery: %+v %v", claimed, err)
	}
}

func TestContinuationRenewsApprovalAfterRepeatedInterruption(t *testing.T) {
	st := runtimeStore(t)
	opts, calls, source := interruptedWorkflow(t, st, true)
	rootID := source.RunID
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	t.Setenv("SDK_FIXTURE_CONTINUATION", "1")
	for attempt := 0; attempt < 2; attempt++ {
		review, err := ReviewContinuation(t.Context(), st, opts.RunsDir, source.RunID)
		if err != nil || !review.Eligible || len(review.RenewApprovals) != 1 || len(review.Reused) != 1 {
			t.Fatalf("review %d: %+v %v", attempt, review, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		started := make(chan error, 1)
		child := Options{RunID: NewRunID("renewed"), RunsDir: opts.RunsDir, Started: started}
		type completion struct {
			result Result
			err    error
		}
		done := make(chan completion, 1)
		go func() {
			result, err := RunContinuation(ctx, st, child, source.RunID, review.ReviewToken)
			done <- completion{result, err}
		}()
		select {
		case err := <-started:
			if err != nil {
				cancel()
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("continuation startup stalled")
		}
		approvalID := child.RunID + ":approval:approval"
		waitContinuation(t, func() bool {
			record, err := st.GetRecord(t.Context(), "approvals", approvalID)
			return err == nil && record.State == "pending"
		})
		data, _ := os.ReadFile(calls)
		if string(data) != "explore\nreview\n" {
			cancel()
			t.Fatalf("model ran before renewed approval: %s", data)
		}
		if _, err := st.DecideSDKApproval(t.Context(), source.RunID+":approval:approval", "approved"); !errors.Is(err, store.ErrSDKApprovalExpired) {
			cancel()
			t.Fatalf("old approval accepted: %v", err)
		}
		if attempt == 0 {
			cancel()
		} else if _, err := st.DecideSDKApproval(t.Context(), approvalID, "approved"); err != nil {
			cancel()
			t.Fatal(err)
		}
		select {
		case completed := <-done:
			cancel()
			want := "passed"
			if attempt == 0 {
				want = "interrupted"
			}
			if completed.err != nil || completed.result.State != want {
				t.Fatalf("continuation %d: %+v", attempt, completed)
			}
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("continuation did not finish")
		}
		source, err = st.GetRun(t.Context(), child.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if store.SDKDeliveryRoot(source) != rootID {
			t.Fatalf("continued delivery lost frozen root: %s", source.Options)
		}
	}
	data, _ := os.ReadFile(calls)
	if string(data) != "explore\nreview\nreview\n" {
		t.Fatalf("renewal reran work: %s", data)
	}
	if !strings.Contains(string(source.Doc), `"reuseThrough": 2`) {
		t.Fatalf("reusable prefix lost: %s", source.Doc)
	}
}
