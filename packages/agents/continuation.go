package agentchecks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

var ErrContinuationBlocked = errors.New("continuation is unavailable")

type ContinuationStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}
type ContinuationPlan struct {
	Checkpoint            json.RawMessage    `json:"checkpoint"`
	Completed             []ContinuationStep `json:"completed"`
	Reused                []ContinuationStep `json:"reused"`
	Restart               *ContinuationStep  `json:"restart"`
	RenewApprovals        []ContinuationStep `json:"renewApprovals"`
	RuntimeFingerprint    string             `json:"runtimeFingerprint"`
	DefinitionFingerprint string             `json:"definitionFingerprint"`
	Usage                 map[string]int64   `json:"usage"`
	Attempts              int                `json:"attempts"`
}

// ReviewContinuation reads only the declared source's pinned evidence. It does
// not copy files, create a run or start an SDK conversation.
func ReviewContinuation(ctx context.Context, request, checkpoint json.RawMessage, source string) (ContinuationPlan, error) {
	return continuationOperation(ctx, "inspect", request, checkpoint, source, "")
}

// PrepareContinuation copies independently verified completed evidence into an
// application-owned fresh workspace. Call while holding source deletion/ownership
// exclusion; the SDK never copies partial task output or old approval documents.
func PrepareContinuation(ctx context.Context, request, checkpoint json.RawMessage, source, destination string) (ContinuationPlan, error) {
	return continuationOperation(ctx, "prepare", request, checkpoint, source, destination)
}
func continuationOperation(ctx context.Context, operation string, request, checkpoint json.RawMessage, source, destination string) (ContinuationPlan, error) {
	var plan ContinuationPlan
	root, err := RuntimeDirectory()
	if err != nil {
		return plan, fmt.Errorf("%w: %w", ErrValidationUnavailable, err)
	}
	fingerprint, err := ReadRuntimeFingerprint(root)
	if err != nil || fingerprint != RuntimeFingerprint() {
		return plan, fmt.Errorf("%w: install matching SDK sources", ErrValidationUnavailable)
	}
	body, err := json.Marshal(struct {
		Operation          string          `json:"operation"`
		Request            json.RawMessage `json:"request"`
		Checkpoint         json.RawMessage `json:"checkpoint"`
		SourceWorkspace    string          `json:"sourceWorkspace"`
		Workspace          string          `json:"workspace,omitempty"`
		RuntimeFingerprint string          `json:"runtimeFingerprint"`
	}{operation, request, checkpoint, source, destination, fingerprint})
	if err != nil {
		return plan, err
	}
	if len(body) > validationLimit {
		return plan, fmt.Errorf("%w: continuation record exceeds 1 MiB", ErrContinuationBlocked)
	}
	// Large pinned outputs may take time to hash/copy; cancellation is cooperative
	// first, then forcibly bounded if the worker cannot finish shutdown.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "src", "continuation-worker.mjs"))
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(body)
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	var stdout, stderr boundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return plan, ctx.Err()
		}
		return plan, fmt.Errorf("%w: continuation worker failed", ErrValidationUnavailable)
	}
	if stdout.exceeded || stderr.exceeded {
		return plan, fmt.Errorf("%w: continuation response exceeds 1 MiB", ErrValidationUnavailable)
	}
	var response struct {
		OK                        *bool             `json:"ok"`
		Reason                    string            `json:"reason"`
		Plan                      *ContinuationPlan `json:"plan"`
		RuntimeFingerprint        string            `json:"runtimeFingerprint"`
		RuntimeFingerprintVersion int               `json:"runtimeFingerprintVersion"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return plan, fmt.Errorf("%w: invalid continuation response: %v", ErrValidationUnavailable, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF || response.OK == nil || response.RuntimeFingerprint != fingerprint || response.RuntimeFingerprintVersion != RuntimeFingerprintVersion {
		return plan, fmt.Errorf("%w: invalid continuation response identity", ErrValidationUnavailable)
	}
	if !*response.OK {
		if response.Reason == "" {
			return plan, fmt.Errorf("%w: missing continuation reason", ErrValidationUnavailable)
		}
		return plan, fmt.Errorf("%w: %s", ErrContinuationBlocked, response.Reason)
	}
	if response.Plan == nil || response.Plan.RuntimeFingerprint != fingerprint || len(response.Plan.DefinitionFingerprint) != 64 {
		return plan, fmt.Errorf("%w: missing continuation plan identity", ErrValidationUnavailable)
	}
	return *response.Plan, nil
}
