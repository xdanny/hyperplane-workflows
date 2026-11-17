package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

type WorkflowContinuation struct {
	Checkpoint json.RawMessage `json:"checkpoint"`
}

type WorkflowRequest struct {
	Continuation       *WorkflowContinuation `json:"continuation,omitempty"`
	RuntimeFingerprint string                `json:"runtimeFingerprint,omitempty"`
	Nodes              []sdkworkflow.Node    `json:"nodes,omitempty"`
	Workflow           string                `json:"workflow"`
	Workspace          string                `json:"workspace"`
	Domain             string                `json:"domain,omitempty"`
	Instruction        string                `json:"instruction,omitempty"`
	Check              []string              `json:"check,omitempty"`
	// Slots are the reusable-input values this launch was given. They are
	// recorded with the frozen request so a run says which inputs produced it;
	// the instructions already contain the substituted values.
	Slots map[string]string `json:"slots,omitempty"`
	// SlotLabels are the labels the saved workflow gave those inputs, frozen with
	// the request so a later reader sees "Week to cover" rather than the id
	// "weekToCover". Renaming a label afterwards does not relabel an old run.
	SlotLabels map[string]string `json:"slotLabels,omitempty"`
	Model      string            `json:"model"`
	AutoRepair bool              `json:"autoRepair"`
	MaxTokens  int               `json:"maxTokens"`
}

type WorkflowResult struct {
	RuntimeFingerprint        string          `json:"runtimeFingerprint,omitempty"`
	RuntimeFingerprintVersion int             `json:"runtimeFingerprintVersion,omitempty"`
	DefinitionFingerprint     string          `json:"definitionFingerprint,omitempty"`
	ReuseThrough              int             `json:"reuseThrough,omitempty"`
	Verification              string          `json:"verification,omitempty"`
	Nodes                     json.RawMessage `json:"nodes,omitempty"`
	ThreadID                  string          `json:"threadId"`
	Passed                    bool            `json:"passed"`
	Attempts                  int             `json:"attempts"`
	Findings                  []string        `json:"findings"`
	Text                      string          `json:"text"`
	Usage                     struct {
		Input  int `json:"input_tokens"`
		Cached int `json:"cached_input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
}

type WorkflowHooks struct {
	Persist func(json.RawMessage) error
	Approve func(context.Context, json.RawMessage) (string, error)
}

// RunWorkflow executes the application workflow in the official SDK. A
// checkpoint is acknowledged only after persist returns successfully. The
// callback must be bounded; it runs outside the model's writable workspace.
func RunWorkflow(ctx context.Context, req WorkflowRequest, logPath string, hooks WorkflowHooks) (WorkflowResult, error) {
	var result WorkflowResult
	if hooks.Persist == nil {
		return result, fmt.Errorf("workflow persistence is required")
	}
	worker, err := sdkWorkerPath()
	if err != nil {
		return result, err
	}
	expected := agentchecks.RuntimeFingerprint()
	if req.RuntimeFingerprint != "" && req.RuntimeFingerprint != expected {
		return result, fmt.Errorf("workflow runtime revision differs from this backend")
	}
	actual, err := agentchecks.ReadRuntimeFingerprint(filepath.Dir(filepath.Dir(worker)))
	if err != nil || actual != expected {
		return result, fmt.Errorf("SDK runtime differs from the backend; rebuild and install them together")
	}
	req.RuntimeFingerprint = expected
	if req.Workflow == "checked-task" {
		workspace, err := filepath.EvalSymlinks(req.Workspace)
		if err != nil {
			return result, err
		}
		runtimeRoot, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(worker)))
		if err != nil {
			return result, err
		}
		if pathsOverlap(workspace, runtimeRoot) {
			return result, fmt.Errorf("task target must be disjoint from the SDK runtime")
		}
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(ctx, workflowTimeout(req))
	defer cancel()
	cmd := ownedCommand(ctx, "node", filepath.Join(filepath.Dir(worker), "workflow-worker.mjs"))
	input, err := cmd.StdinPipe()
	if err != nil {
		return result, err
	}
	defer input.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	complete := false
	seq := 0
	output := &workflowLines{handle: func(line []byte) error {
		var event struct {
			Type   string          `json:"type"`
			Seq    int             `json:"seq"`
			State  json.RawMessage `json:"state"`
			Result WorkflowResult  `json:"result"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		switch event.Type {
		case "hyperplane.checkpoint":
			if event.Seq != seq+1 || complete {
				return fmt.Errorf("invalid checkpoint sequence")
			}
			if err := verifyWorkflowRevision(event.State, expected); err != nil {
				return err
			}
			if err := hooks.Persist(event.State); err != nil {
				return err
			}
			seq = event.Seq
			_, err := fmt.Fprintf(input, "{\"ack\":%d}\n", seq)
			return err
		case "hyperplane.approval":
			if event.Seq != seq+1 || complete || hooks.Approve == nil {
				return fmt.Errorf("invalid approval request")
			}
			if err := verifyWorkflowRevision(event.State, expected); err != nil {
				return err
			}
			decision, err := hooks.Approve(ctx, event.State)
			if err != nil {
				return err
			}
			if decision != "approved" && decision != "rejected" {
				return fmt.Errorf("invalid approval decision")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			seq = event.Seq
			return json.NewEncoder(input).Encode(map[string]any{"ack": seq, "decision": decision})

		case "hyperplane.workflow_result":
			if complete || seq == 0 {
				return fmt.Errorf("invalid workflow completion")
			}
			if event.Result.RuntimeFingerprint != expected || event.Result.RuntimeFingerprintVersion != agentchecks.RuntimeFingerprintVersion {
				return fmt.Errorf("SDK result runtime revision differs from launch")
			}
			result, complete = event.Result, true
		}
		return nil
	}, cancel: cancel, log: log}
	cmd.Stdout = output
	if err := cmd.Start(); err != nil {
		return result, err
	}
	defer cmd.Cancel() // reap remaining descendants even if the worker exits first
	if err := json.NewEncoder(input).Encode(req); err != nil {
		cancel()
	}
	err = cmd.Wait()
	if output.err != nil {
		return result, output.err
	}
	if err != nil {
		return result, fmt.Errorf("SDK workflow: %w: %s", err, stderr.String())
	}
	if !complete || len(output.pending) != 0 {
		return result, fmt.Errorf("SDK workflow ended without a complete result")
	}
	return result, nil
}

func verifyWorkflowRevision(raw json.RawMessage, expected string) error {
	var state struct {
		RuntimeFingerprint        string
		RuntimeFingerprintVersion int
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.RuntimeFingerprint != expected || state.RuntimeFingerprintVersion != agentchecks.RuntimeFingerprintVersion {
		return fmt.Errorf("SDK checkpoint runtime revision differs from launch")
	}
	return nil
}

type workflowLines struct {
	pending []byte
	handle  func([]byte) error
	cancel  context.CancelFunc
	err     error
	log     io.Writer
}

func (w *workflowLines) Write(p []byte) (int, error) {
	if w.log != nil {
		if _, err := w.log.Write(p); err != nil {
			w.err = err
			w.cancel()
			return 0, err
		}
	}
	w.pending = append(w.pending, p...)
	for w.err == nil {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		if i > 1_000_000 {
			w.err = fmt.Errorf("SDK event exceeds 1 MB")
			break
		}
		w.err = w.handle(w.pending[:i])
		w.pending = w.pending[i+1:]
	}
	if len(w.pending) > 1_000_000 {
		w.err = fmt.Errorf("SDK event exceeds 1 MB")
	}
	if w.err != nil {
		w.cancel()
		return 0, w.err
	}
	return len(p), nil
}

func pathsOverlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || filepath.IsLocal(rel) {
			return true
		}
	}
	return false
}

// Sequential workflows share an aggregate limit. Explicit token limits are
// enforced by the SDK worker; time includes human approval waits and is bounded.
func workflowTimeout(req WorkflowRequest) time.Duration {
	steps := len(req.Nodes)
	if steps < 1 {
		steps = 1
	}
	if steps > 20 {
		steps = 20
	}
	return DefaultTimeout * time.Duration(steps)
}
