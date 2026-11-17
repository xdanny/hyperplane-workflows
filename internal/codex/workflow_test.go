package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

func TestWorkflowCheckpointsFenceCorrections(t *testing.T) {
	exe, _ := filepath.Abs("testdata/ideator")
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", exe)
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			dir := t.TempDir()
			workspace := filepath.Join(dir, "workspace")
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(dir, "calls")
			t.Setenv("SDK_FIXTURE_CALLS", calls)
			var phases []string
			result, err := RunWorkflow(context.Background(), WorkflowRequest{Workflow: "app-ideator", Workspace: workspace, AutoRepair: true, MaxTokens: 300000}, filepath.Join(dir, "sdk.log"), WorkflowHooks{Persist: func(raw json.RawMessage) error {
				var state struct {
					Phase    string   `json:"phase"`
					Findings []string `json:"findings"`
					ThreadID string   `json:"threadId"`
				}
				if err := json.Unmarshal(raw, &state); err != nil {
					return err
				}
				phases = append(phases, state.Phase)
				if state.ThreadID != "fixture-ideator-thread" {
					return fmt.Errorf("missing thread identity")
				}
				if state.Phase == "correction_pending" {
					data, _ := os.ReadFile(calls)
					if string(data) != "start\n" || len(state.Findings) == 0 {
						return fmt.Errorf("correction began before findings were committed")
					}
					if reject {
						return fmt.Errorf("database unavailable")
					}
				}
				return nil
			}})
			data, _ := os.ReadFile(calls)
			if reject {
				if err == nil || !strings.Contains(err.Error(), "database unavailable") || string(data) != "start\n" {
					t.Fatalf("uncommitted correction: %s %+v %v", data, result, err)
				}
			} else if err != nil || !result.Passed || result.Attempts != 2 || result.Usage.Input != 34 || string(data) != "start\nresume\n" {
				t.Fatalf("result=%+v phases=%v calls=%s error=%v", result, phases, data, err)
			}
		})
	}
}

type failedWorkflowLog struct{}

func (failedWorkflowLog) Write([]byte) (int, error) { return 0, fmt.Errorf("disk full") }

func TestWorkflowLogFailureCancelsBeforeAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := workflowLines{log: failedWorkflowLog{}, cancel: cancel, handle: func([]byte) error { t.Fatal("acknowledged an unrecorded event"); return nil }}
	if _, err := w.Write([]byte("{\"type\":\"hyperplane.checkpoint\"}\n")); err == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("log failure did not cancel immediately: %v %v", err, ctx.Err())
	}
}

func TestWorkflowRefusesTargetInsideRuntime(t *testing.T) {
	worker, err := sdkWorkerPath()
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunWorkflow(context.Background(), WorkflowRequest{Workflow: "checked-task", Workspace: filepath.Dir(worker)}, filepath.Join(t.TempDir(), "log"), WorkflowHooks{Persist: func(json.RawMessage) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "disjoint") {
		t.Fatalf("runtime target accepted: %v", err)
	}
}

func TestWorkflowTimeLimitCoversSequentialSteps(t *testing.T) {
	for _, steps := range []int{0, 1, 9, 20, 21} {
		count := steps
		if count < 1 {
			count = 1
		}
		if count > 20 {
			count = 20
		}
		got := workflowTimeout(WorkflowRequest{Nodes: make([]sdkworkflow.Node, steps)})
		if got != DefaultTimeout*time.Duration(count) {
			t.Fatalf("%d steps: %s", steps, got)
		}
	}
}

func TestWorkflowRejectsRuntimeMismatchBeforeExecution(t *testing.T) {
	worker, err := sdkWorkerPath()
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Dir(filepath.Dir(worker))
	data, err := os.ReadFile(filepath.Join(installed, "runtime-files.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct{ Files []string }
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"src/workflows/task.mjs", "checks.json", "package-lock.json", "missing", "request"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			pkg := filepath.Join(root, "packages", "agents")
			for _, name := range inventory.Files {
				data, err := os.ReadFile(filepath.Join(installed, name))
				if err != nil {
					t.Fatal(err)
				}
				dest := filepath.Join(pkg, name)
				if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "missing":
				if err := os.Remove(filepath.Join(pkg, "src/workflows/task.mjs")); err != nil {
					t.Fatal(err)
				}
			case "request":
			default:
				path := filepath.Join(pkg, change)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HYPERPLANE_PLATFORM_ROOT", root)
			for _, workflow := range []string{"node-workflow", "checked-task", "app-ideator"} {
				t.Run(workflow, func(t *testing.T) {
					called := false
					log := filepath.Join(t.TempDir(), "run.log")
					req := WorkflowRequest{Workflow: workflow}
					want := "SDK runtime differs"
					if change == "request" {
						req.RuntimeFingerprint = strings.Repeat("0", 64)
						want = "workflow runtime revision differs"
					}
					_, err := RunWorkflow(context.Background(), req, log, WorkflowHooks{Persist: func(json.RawMessage) error { called = true; return nil }})
					if err == nil || !strings.Contains(err.Error(), want) || called {
						t.Fatalf("mismatch was not rejected before execution: called=%v err=%v", called, err)
					}
					if _, err := os.Stat(log); !os.IsNotExist(err) {
						t.Fatal("created execution log before runtime mismatch rejection")
					}
				})
			}
		})
	}
}

func TestWorkflowRevisionRejectsChangedAndUnversionedReceipts(t *testing.T) {
	expected := agentchecks.RuntimeFingerprint()
	for _, raw := range []string{
		`{}`, `null`, `{`,
		fmt.Sprintf(`{"runtimeFingerprint":%q}`, expected),
		fmt.Sprintf(`{"runtimeFingerprint":%q,"runtimeFingerprintVersion":2}`, expected),
		`{"runtimeFingerprint":"changed","runtimeFingerprintVersion":1}`,
	} {
		if err := verifyWorkflowRevision(json.RawMessage(raw), expected); err == nil {
			t.Fatalf("accepted mismatched receipt: %s", raw)
		}
	}
	valid, _ := json.Marshal(map[string]any{"runtimeFingerprint": expected, "runtimeFingerprintVersion": agentchecks.RuntimeFingerprintVersion})
	if err := verifyWorkflowRevision(valid, expected); err != nil {
		t.Fatal(err)
	}
}
