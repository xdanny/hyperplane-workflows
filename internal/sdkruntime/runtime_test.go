package sdkruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/contract"
	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
	"github.com/xdanny/hyperplane-workflows/internal/events"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

func TestMain(m *testing.M) {
	cleanup := dbtest.RedirectToTestDB("sdkruntime")
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func runtimeStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL") // Only TestMain may set this for tests.
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	if err := dbtest.GuardTestDSN(dsn); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

func fixture(t *testing.T, name, mode string) {
	t.Helper()
	exe, err := filepath.Abs(filepath.Join("../codex/testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", exe)
	t.Setenv("SDK_FIXTURE_MODE", mode)
	t.Setenv("HYPERPLANE_MODEL", "fixture-model")
}

// Actual SDK transport and PostgreSQL, with only model responses replaced.
func TestRunPersistsSDKRequestAndDelivery(t *testing.T) {
	st := runtimeStore(t)
	saved, err := st.SaveSDKWorkflow(t.Context(), store.SDKWorkflow{Name: "Original setup", Kind: "app-ideator", Instruction: "Stored settings must never replace the submitted draft"})
	if err != nil {
		t.Fatal(err)
	}
	for _, workflow := range []string{"app-ideator", "checked-task", "node-workflow"} {
		t.Run(workflow, func(t *testing.T) {
			opts := Options{RunID: NewRunID(workflow), RunsDir: t.TempDir(), Goal: "A reviewed request", Project: "runtime-test", TokenBudget: 12345, Bus: events.NewBus()}
			opts.SourceWorkflowID, opts.WorkflowName = saved.ID, "Edited "+workflow
			live, unsubscribe := opts.Bus.Subscribe(opts.RunID)
			defer unsubscribe()
			var result Result
			var err error
			switch workflow {
			case "app-ideator":
				fixture(t, "ideator", "recovery")
				opts.AutoRepair = true
				opts.Inputs = map[string]string{"domain": "cycling tools"}
				result, err = RunAppIdeator(t.Context(), st, opts)
			case "checked-task":
				fixture(t, "task", "ordinary")
				opts.TargetRoot = t.TempDir()
				result, err = RunCheckedTask(t.Context(), st, opts, "Write answer.txt with 42", nil)
			case "node-workflow":
				fixture(t, "nodes", "ordinary")
				result, err = RunNodeWorkflow(t.Context(), st, opts, "Compare options", []sdkworkflow.Node{
					{ID: "explore", Title: "Explore", Instruction: "Find options"},
					{ID: "review", Title: "Review", Instruction: "Compare options"},
				})
			}
			if err != nil || result.State != "passed" || result.RunID != opts.RunID {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			run, err := st.GetRun(t.Context(), result.RunID)
			if err != nil {
				t.Fatal(err)
			}
			var launch map[string]json.RawMessage
			if err := json.Unmarshal(run.Options, &launch); err != nil {
				t.Fatal(err)
			}
			var identity struct{ Key, Name, SourceWorkflowID, DefinitionKey string }
			if err := json.Unmarshal(launch["projectWorkflow"], &identity); err != nil {
				t.Fatal(err)
			}
			if identity.Key != "saved:"+saved.ID || identity.Name != opts.WorkflowName || identity.SourceWorkflowID != saved.ID {
				t.Fatalf("lost source association: %s", launch["projectWorkflow"])
			}
			// Projection of the actual persisted SDK request matches its admission
			// identity even after managed folders/model/budget have been populated.
			projected, err := st.PrepareProjectWorkflow(t.Context(), launch["request"], "", "", run.Goal)
			if err != nil {
				t.Fatal(err)
			}
			var actual struct{ DefinitionKey string }
			if err := json.Unmarshal(projected, &actual); err != nil || actual.DefinitionKey != identity.DefinitionKey {
				t.Fatalf("admission/request projection differs: %s %s %v", launch["projectWorkflow"], projected, err)
			}
			if _, exists := launch["spec"]; exists {
				t.Fatal("SDK launch contains synthetic graph spec")
			}
			var request codex.WorkflowRequest
			if err := json.Unmarshal(launch["request"], &request); err != nil {
				t.Fatal(err)
			}
			expectedRevision := agentchecks.RuntimeFingerprint()
			if request.RuntimeFingerprint != expectedRevision || len(expectedRevision) != 64 {
				t.Fatalf("launch did not pin SDK revision: request=%q expected=%q", request.RuntimeFingerprint, expectedRevision)
			}
			if request.Workspace == "" || !filepath.IsAbs(request.Workspace) {
				t.Fatalf("launch did not pin an absolute workspace: %q", request.Workspace)
			}
			assertRevision := func(label string, raw json.RawMessage) {
				t.Helper()
				var checkpoint struct {
					RuntimeFingerprint        string `json:"runtimeFingerprint"`
					RuntimeFingerprintVersion int    `json:"runtimeFingerprintVersion"`
				}
				if err := json.Unmarshal(raw, &checkpoint); err != nil {
					t.Fatal(err)
				}
				if checkpoint.RuntimeFingerprint != request.RuntimeFingerprint || checkpoint.RuntimeFingerprintVersion != agentchecks.RuntimeFingerprintVersion {
					t.Fatalf("%s lost launch revision: %s", label, raw)
				}
			}
			var document struct {
				SDK json.RawMessage `json:"sdk"`
			}
			if err := json.Unmarshal(run.Doc, &document); err != nil {
				t.Fatal(err)
			}
			assertRevision("run checkpoint", document.SDK)
			assertRevision("run summary", run.Summary)
			if request.Workflow != workflow || request.Model != "fixture-model" || request.MaxTokens != 12345 || request.AutoRepair != opts.AutoRepair || run.DAG != workflow || run.Goal != opts.Goal || run.Project != opts.Project {
				t.Fatalf("launch changed request: %+v run=%+v", request, run)
			}
			if workflow == "app-ideator" && request.Domain != "cycling tools" || workflow == "checked-task" && request.Instruction != "Write answer.txt with 42" || workflow == "node-workflow" && (request.Instruction != "Compare options" || len(request.Nodes) != 2 || request.Nodes[1].ID != "review") {
				t.Fatalf("missing actual workflow inputs: %+v", request)
			}
			nodes, err := st.ListNodes(t.Context(), result.RunID)
			if err != nil || len(nodes) != 1 || nodes[0].NodeID != "delivery" || nodes[0].State != "passed" {
				t.Fatalf("delivery=%+v error=%v", nodes, err)
			}
			assertRevision("delivery checkpoint", nodes[0].Doc)
			var envelope contract.Envelope
			if err := json.Unmarshal(nodes[0].Output, &envelope); err != nil || !envelope.OK || envelope.Provenance.Engine != "codex-sdk" {
				t.Fatalf("invalid envelope: %s error=%v", nodes[0].Output, err)
			}
			if workflow == "checked-task" {
				if !strings.Contains(string(run.Summary), `"verification": "not_requested"`) {
					t.Fatalf("unverified task lost its status: %s", run.Summary)
				}
			} else if len(envelope.Outputs) != 2 {
				t.Fatalf("missing artifact envelopes: %s", nodes[0].Output)
			}
			if workflow == "app-ideator" && (nodes[0].Attempt != 2 || !strings.Contains(string(run.Summary), "fixture-ideator-thread")) {
				t.Fatalf("same-session correction lost: %s", run.Summary)
			}
			journal, err := st.ListEvents(t.Context(), result.RunID, 0, 100)
			if err != nil || len(journal) < 3 || journal[len(journal)-1].Type != "run.done" {
				t.Fatalf("journal=%+v error=%v", journal, err)
			}
			for _, stored := range journal {
				assertRevision("committed journal checkpoint", stored.Data)
				select {
				case published := <-live:
					if published.Seq != stored.Seq || published.Type != stored.Type {
						t.Fatalf("uncommitted event: %+v vs %+v", published, stored)
					}
				default:
					t.Fatal("missing committed live event")
				}
			}
		})
	}
}

func TestStartupAcknowledgementAndCancellation(t *testing.T) {
	st := runtimeStore(t)
	fixture(t, "ideator", "hang")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error)
	opts := Options{RunID: NewRunID("cancel"), RunsDir: t.TempDir(), Started: started}
	type completion struct {
		result Result
		err    error
	}
	done := make(chan completion, 1)
	go func() { result, err := RunAppIdeator(ctx, st, opts); done <- completion{result, err} }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("startup was not acknowledged")
	}
	run, err := st.GetRun(t.Context(), opts.RunID)
	if err != nil || run.State != "running" || !strings.Contains(string(run.Doc), `"sdk"`) {
		t.Fatalf("acknowledged before startup persisted: %+v %v", run, err)
	}
	// Wait for the owned SDK child, so cancellation exercises process termination.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(string(run.Doc), "fixture-ideator-thread") {
		if time.Now().After(deadline) {
			t.Fatal("SDK child did not start")
		}
		time.Sleep(20 * time.Millisecond)
		run, err = st.GetRun(t.Context(), opts.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case completed := <-done:
		if completed.err != nil || completed.result.State != "interrupted" {
			t.Fatalf("cancel: %+v", completed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned SDK process did not stop")
	}
	run, err = st.GetRun(t.Context(), opts.RunID)
	if err != nil || run.State != "interrupted" || !strings.Contains(string(run.Summary), "fixture-ideator-thread") {
		t.Fatalf("cancelled run lost checkpoint: %+v %v", run, err)
	}
	select {
	case err := <-started:
		t.Fatalf("duplicate acknowledgement: %v", err)
	default:
	}
}

func TestStartupFailureAcknowledgedWithoutModel(t *testing.T) {
	for _, workflow := range []string{"app-ideator", "checked-task", "node-workflow"} {
		t.Run(workflow, func(t *testing.T) {
			started := make(chan error, 2)
			opts := Options{Started: started, Inputs: map[string]string{"unknown": "input"}}
			var err error
			switch workflow {
			case "app-ideator":
				_, err = RunAppIdeator(t.Context(), nil, opts)
			case "checked-task":
				_, err = RunCheckedTask(t.Context(), nil, opts, "", nil)
			case "node-workflow":
				_, err = RunNodeWorkflow(t.Context(), nil, opts, "", nil)
			}
			if err == nil || len(started) != 1 || !errors.Is(<-started, err) {
				t.Fatalf("startup error was not acknowledged exactly once: %v", err)
			}
		})
	}
}

func TestDatabaseOwnershipStopsActiveSDKTurn(t *testing.T) {
	for _, action := range []string{"disconnect", "cancel-from-another-process"} {
		t.Run(action, func(t *testing.T) {
			st := runtimeStore(t)
			fixture(t, "ideator", "hang")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan error, 1)
			opts := Options{RunID: NewRunID("disconnect"), RunsDir: t.TempDir(), Started: started}
			done := make(chan Result, 1)
			go func() {
				result, err := RunAppIdeator(ctx, st, opts)
				if err != nil {
					t.Errorf("persist interrupted result: %v", err)
				}
				done <- result
			}()
			select {
			case err := <-started:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("startup timeout")
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				run, err := st.GetRun(t.Context(), opts.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(run.Doc), "fixture-ideator-thread") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("SDK turn did not start")
				}
				time.Sleep(20 * time.Millisecond)
			}
			// A second application's startup must preserve this live owner.
			if _, err := st.ReconcileOrphans(t.Context()); err != nil {
				t.Fatal(err)
			}
			run, err := st.GetRun(t.Context(), opts.RunID)
			if err != nil || run.State != "running" {
				t.Fatalf("active run reconciled: %+v %v", run, err)
			}
			wantedReason := "Execution cancelled"
			if action == "cancel-from-another-process" {
				if err := st.CancelRun(t.Context(), opts.RunID); err != nil {
					t.Fatal(err)
				}
			} else {
				wantedReason = "Execution owner disconnected"
				var stopped bool
				err = st.Pool.QueryRow(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype='advisory' AND granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
		AND classid::bigint=((hashtextextended($1,684795135) >> 32) & 4294967295)
		AND objid::bigint=(hashtextextended($1,684795135) & 4294967295) AND objsubid=1`, opts.RunID).Scan(&stopped)
				if err != nil || !stopped {
					t.Fatalf("disconnect owned session: %v %v", stopped, err)
				}
			}
			select {
			case result := <-done:
				if result.State != "interrupted" {
					t.Fatalf("lost owner result: %+v", result)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("SDK turn continued without ownership")
			}
			run, err = st.GetRun(t.Context(), opts.RunID)
			if err != nil || !strings.Contains(string(run.Summary), wantedReason) || !strings.Contains(string(run.Summary), "fixture-ideator-thread") {
				t.Fatalf("lost disconnect evidence: %+v %v", run, err)
			}
		})
	}
}

func TestApprovalPinsDocumentsBeforeContinuation(t *testing.T) {
	st := runtimeStore(t)
	fixture(t, "nodes", "ordinary")
	t.Setenv("SDK_FIXTURE_APPROVAL", "1")
	for _, mode := range []string{"approved", "rejected", "changed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opts := Options{RunID: NewRunID(mode), RunsDir: t.TempDir()}
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			done := make(chan Result, 1)
			errChan := make(chan error, 1)
			go func() {
				result, err := RunNodeWorkflow(ctx, st, opts, "Compare options", []sdkworkflow.Node{
					{ID: "explore", Title: "Explore", Instruction: "Find options"},
					{ID: "approval", Kind: "approval", Title: "Approve options", Instruction: "Review these options"},
					{ID: "review", Title: "Review", Instruction: "Compare approved options"},
				})
				errChan <- err
				done <- result
			}()
			approvalID := opts.RunID + ":approval:approval"
			var record store.Record
			deadline := time.Now().Add(10 * time.Second)
			for {
				var err error
				record, err = st.GetRecord(t.Context(), "approvals", approvalID)
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("approval was not persisted")
				}
				time.Sleep(20 * time.Millisecond)
			}
			path := filepath.Join(opts.RunsDir, opts.RunID, "nodes/delivery/OUTPUTS/explore.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(record.Doc), fmt.Sprintf("%x", sha256.Sum256(data))) {
				t.Fatalf("approval lacks actual file hash: %s", record.Doc)
			}
			data, _ = os.ReadFile(calls)
			if string(data) != "explore\n" {
				t.Fatalf("advanced before approval: %s", data)
			}
			if mode == "changed" {
				if err := os.WriteFile(path, []byte("changed after review"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cancelled" {
				cancel()
			} else {
				decision := "approved"
				if mode == "rejected" {
					decision = "rejected"
				}
				if _, err := st.DecideSDKApproval(t.Context(), approvalID, decision); err != nil {
					t.Fatal(err)
				}
			}
			want := "failed"
			if mode == "approved" {
				want = "passed"
			}
			if mode == "cancelled" {
				want = "interrupted"
			}
			select {
			case result := <-done:
				if result.State != want {
					t.Fatalf("approval result=%+v error=%v", result, <-errChan)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("approval did not finish")
			}
			data, _ = os.ReadFile(calls)
			wantCalls := "explore\n"
			if mode == "approved" {
				wantCalls += "review\n"
			}
			if string(data) != wantCalls {
				t.Fatalf("wrong continuation: %s", data)
			}
			if _, err := st.DecideSDKApproval(t.Context(), approvalID, "approved"); !errors.Is(err, store.ErrSDKApprovalExpired) {
				t.Fatalf("accepted stale decision: %v", err)
			}
			record, err = st.GetRecord(t.Context(), "approvals", approvalID)
			if err != nil || record.State == "pending" {
				t.Fatalf("pending approval after completion: %+v %v", record, err)
			}
		})
	}
}
