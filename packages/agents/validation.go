package agentchecks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrValidationUnavailable means validation could not establish an answer. It
// must not be presented as a defect in the user's workflow.
var ErrValidationUnavailable = errors.New("SDK validation unavailable")

// ValidationIssue is an SDK-owned semantic rejection of a workflow.
type ValidationIssue struct {
	NodeID  string `json:"nodeId,omitempty"`
	Message string `json:"message"`
}

func (i *ValidationIssue) Error() string {
	if i.NodeID != "" {
		return i.NodeID + ": " + i.Message
	}
	return i.Message
}

type ValidationReport struct {
	Valid                     bool             `json:"valid"`
	Issue                     *ValidationIssue `json:"issue,omitempty"`
	RuntimeFingerprint        string           `json:"runtimeFingerprint"`
	RuntimeFingerprintVersion int              `json:"runtimeFingerprintVersion"`
}

// RuntimeDirectory resolves application-owned SDK code independently of a
// workflow workspace. Both execution and pure validation use this installation.
func RuntimeDirectory() (string, error) {
	if root := os.Getenv("HYPERPLANE_PLATFORM_ROOT"); root != "" {
		dir, err := filepath.Abs(filepath.Join(root, "packages", "agents"))
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(dir, "src", "worker.mjs")); err != nil {
			return "", fmt.Errorf("SDK worker: %w; run make sdk-install", err)
		}
		return dir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		dir := filepath.Join(cwd, "packages", "agents")
		if _, err := os.Stat(filepath.Join(dir, "src", "worker.mjs")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	return "", fmt.Errorf("Codex SDK worker unavailable; set HYPERPLANE_PLATFORM_ROOT and run make sdk-install")
}

const validationLimit = 1 << 20
const validationTimeout = 5 * time.Second

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n > validationLimit-b.Len() {
		b.exceeded = true
		p = p[:validationLimit-b.Len()]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

// InspectWorkflow runs only the SDK's pure validator. It never starts an agent,
// loads user source files, or grants permissions. Semantic invalidity is a report;
// transport, installation and protocol failures wrap ErrValidationUnavailable.
func InspectWorkflow(ctx context.Context, instruction string, nodes json.RawMessage) (ValidationReport, error) {
	report, err := inspectWorkflow(ctx, instruction, nodes)
	if err != nil {
		return ValidationReport{}, fmt.Errorf("%w: %w", ErrValidationUnavailable, err)
	}
	return report, nil
}
func inspectWorkflow(ctx context.Context, instruction string, nodes json.RawMessage) (ValidationReport, error) {
	var report ValidationReport
	ctx, cancel := context.WithTimeout(ctx, validationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	root, err := RuntimeDirectory()
	if err != nil {
		return report, err
	}
	fingerprint, err := ReadRuntimeFingerprint(root)
	if err != nil {
		return report, err
	}
	if fingerprint != RuntimeFingerprint() {
		return report, fmt.Errorf("installed SDK differs from the backend; rebuild and install matching SDK sources")
	}
	request, err := json.Marshal(struct {
		Instruction        string          `json:"instruction"`
		Nodes              json.RawMessage `json:"nodes"`
		RuntimeFingerprint string          `json:"runtimeFingerprint"`
	}{instruction, nodes, fingerprint})
	if err != nil {
		return report, err
	}
	if len(request) > validationLimit {
		return ValidationReport{Valid: false, Issue: &ValidationIssue{Message: "workflow validation request exceeds 1 MiB"}, RuntimeFingerprint: fingerprint, RuntimeFingerprintVersion: RuntimeFingerprintVersion}, nil
	}
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "src", "validate-worker.mjs"))
	cmd.Dir = root
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(request)
	var stdout, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		return report, fmt.Errorf("SDK validator process: %w", err)
	}
	if stdout.exceeded || stderr.exceeded {
		return report, fmt.Errorf("SDK validator output exceeds 1 MiB")
	}
	var response struct {
		Valid                     *bool            `json:"valid"`
		Issue                     *ValidationIssue `json:"issue,omitempty"`
		RuntimeFingerprint        string           `json:"runtimeFingerprint"`
		RuntimeFingerprintVersion int              `json:"runtimeFingerprintVersion"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return report, fmt.Errorf("invalid SDK validation response: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return report, fmt.Errorf("SDK validator must return one JSON object")
	}
	if response.Valid == nil || (*response.Valid && response.Issue != nil) || (!*response.Valid && (response.Issue == nil || response.Issue.Message == "")) {
		return report, fmt.Errorf("incomplete SDK validation response")
	}
	if response.RuntimeFingerprint != fingerprint || response.RuntimeFingerprintVersion != RuntimeFingerprintVersion {
		return report, fmt.Errorf("SDK validation response revision differs from backend")
	}
	return ValidationReport{Valid: *response.Valid, Issue: response.Issue, RuntimeFingerprint: response.RuntimeFingerprint, RuntimeFingerprintVersion: response.RuntimeFingerprintVersion}, nil
}
