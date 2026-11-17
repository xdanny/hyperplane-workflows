package sdkruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func dagNode(id string, deps ...string) sdkworkflow.Node {
	if deps == nil {
		deps = []string{}
	}
	return sdkworkflow.Node{ID: id, Title: id, Kind: "document", Instruction: "DAG_NODE:" + id, DependsOn: deps}
}

func TestSDKDAGTransportOverlapsBranchesAndWaitsForJoin(t *testing.T) {
	for _, mode := range []string{"ordinary", "fail"} {
		t.Run(mode, func(t *testing.T) {
			st := runtimeStore(t)
			fixture(t, "dag", mode)
			markers := t.TempDir()
			t.Setenv("SDK_FIXTURE_DAG_DIR", markers)
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			opts := Options{RunID: NewRunID("dag-" + mode), RunsDir: t.TempDir(), Goal: "Prove independent SDK branches", TokenBudget: 12345}
			nodes := []sdkworkflow.Node{dagNode("join", "left", "right"), dagNode("right", "root"), dagNode("root"), dagNode("left", "root")}
			done := make(chan error, 1)
			go func() { _, err := RunNodeWorkflow(ctx, st, opts, opts.Goal, nodes); done <- err }()
			waitContinuation(t, func() bool {
				run, err := st.GetRun(t.Context(), opts.RunID)
				if err != nil {
					return false
				}
				var doc struct {
					SDK struct{ Nodes []struct{ ID, Phase string } }
				}
				if json.Unmarshal(run.Doc, &doc) != nil {
					return false
				}
				active := map[string]bool{}
				for _, n := range doc.SDK.Nodes {
					if n.Phase == "running" {
						active[n.ID] = true
					}
				}
				return active["left"] && active["right"]
			})
			if _, err := os.Stat(filepath.Join(markers, "join.started")); !os.IsNotExist(err) {
				t.Fatal("join started before dependencies finished")
			}
			if err := os.WriteFile(filepath.Join(markers, "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if mode == "ordinary" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("SDK branches did not terminate after release/failure")
			}
			run, err := st.GetRun(t.Context(), opts.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ordinary" {
				if run.State != "passed" {
					t.Fatalf("DAG failed: %s %s", run.State, run.Doc)
				}
				var doc struct {
					SDK struct {
						Usage struct {
							Input  int `json:"input_tokens"`
							Output int `json:"output_tokens"`
						}
						Nodes []struct{ ID, Phase, ThreadID string }
					}
				}
				if err := json.Unmarshal(run.Doc, &doc); err != nil {
					t.Fatal(err)
				}
				if doc.SDK.Usage.Input != 68 || doc.SDK.Usage.Output != 8 {
					t.Fatalf("concurrent usage lost or counted twice: %+v", doc.SDK.Usage)
				}
				for _, node := range doc.SDK.Nodes {
					if node.Phase != "passed" {
						t.Fatalf("incomplete step: %+v", node)
					}
				}
			} else {
				if run.State != "failed" {
					t.Fatalf("failure state: %s", run.State)
				}
				for _, name := range []string{"join.started", "right.finished"} {
					if _, err := os.Stat(filepath.Join(markers, name)); !os.IsNotExist(err) {
						t.Fatalf("failed wave continued: %s", name)
					}
				}
			}
		})
	}
}

func TestSDKDAGApprovalPinsForwardDependency(t *testing.T) {
	st := runtimeStore(t)
	fixture(t, "dag", "ordinary")
	t.Setenv("SDK_FIXTURE_DAG_DIR", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	opts := Options{RunID: NewRunID("dag-approval"), RunsDir: t.TempDir(), Goal: "Approve actual dependencies"}
	gate := dagNode("approve", "root")
	gate.Kind = "approval"
	nodes := []sdkworkflow.Node{gate, dagNode("root")}
	done := make(chan error, 1)
	go func() { _, err := RunNodeWorkflow(ctx, st, opts, opts.Goal, nodes); done <- err }()
	id := opts.RunID + ":approve:approval"
	waitContinuation(t, func() bool { _, err := st.GetRecord(t.Context(), "approvals", id); return err == nil })
	run, err := st.GetRun(t.Context(), opts.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SDK struct {
			Approval struct{ Documents []struct{ File string } }
		}
	}
	if err := json.Unmarshal(run.Doc, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.SDK.Approval.Documents) != 1 || doc.SDK.Approval.Documents[0].File != "root.md" {
		t.Fatalf("wrong approved evidence: %s", run.Doc)
	}
	if _, err := st.DecideSDKApproval(t.Context(), id, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	run, err = st.GetRun(t.Context(), opts.RunID)
	if err != nil || run.State != "passed" {
		t.Fatalf("forward approval failed: %s %v", run.State, err)
	}
}

func TestSDKDAGContinuationBlockedBeforeWorkspaceAccess(t *testing.T) {
	st := runtimeStore(t)
	req := codex.WorkflowRequest{Workflow: "node-workflow", Workspace: filepath.Join(t.TempDir(), "never-created"), Nodes: []sdkworkflow.Node{dagNode("root")}}
	options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": req})
	run := store.Run{RunID: NewRunID("dag-no-replay"), DAG: "node-workflow", State: "interrupted", Options: options, Doc: json.RawMessage(`{"sdk":{}}`)}
	if err := st.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	review, err := ReviewContinuation(t.Context(), st, t.TempDir(), run.RunID)
	if err != nil || review.Eligible || !strings.Contains(review.Reason, "explicit dependencies") {
		t.Fatalf("unsafe continuation review: %+v %v", review, err)
	}
	if _, err := os.Stat(req.Workspace); !os.IsNotExist(err) {
		t.Fatal("blocked continuation touched workspace")
	}
}
