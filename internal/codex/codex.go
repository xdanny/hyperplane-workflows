// Package codex is the Go bridge to the official Codex SDK. The SDK owns
// invocation, streaming and conversation continuation; this bridge owns the
// cancellable process group and the existing run evidence contract.
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

// DefaultTimeout bounds a single codex call.
const DefaultTimeout = 10 * time.Minute

// Sandbox modes passed to `codex --sandbox`.
const SandboxReadOnly = "read-only"

// ExecResult is the raw outcome of a subprocess.
type ExecResult struct {
	Status   int
	Stdout   string
	Stderr   string
	TimedOut bool
}

// ExecAsync runs a command with an optional timeout and stdin, returning the
// captured output. It never returns an error — failures are encoded in Status.
func ExecAsync(ctx context.Context, name string, args []string, opts ExecOpts) ExecResult {
	runCtx := ctx
	var cancel context.CancelFunc
	if opts.TimeoutMs > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	cmd := ownedCommand(runCtx, name, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, opts.Env...)
	if opts.Input != "" {
		cmd.Stdin = strings.NewReader(opts.Input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.Stderr += "\n[killed: timeout]"
	}
	if cmd.ProcessState != nil {
		res.Status = cmd.ProcessState.ExitCode()
	} else if err != nil {
		res.Status = -1
		res.Stderr += "\n" + err.Error()
	}
	return res
}

func ownedCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	// A workflow owns the whole subprocess group on both supported systems.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd
}

// ExecOpts configures ExecAsync.
type ExecOpts struct {
	Dir       string
	Env       []string
	TimeoutMs int
	Input     string
}

// Options configures a single codex call.
type Options struct {
	ThreadID         string // explicit SDK conversation continuation; empty starts a new session
	Instruction      string
	ContextFiles     []string
	Images           []string
	OutputSchemaPath string
	Model            string
	Reasoning        string // "low" (default) | "high"
	WebSearch        bool   // enable codex's native live web_search tool
	// OSS selects a local model provider for this read-only SDK conversation.
	// The provider must support the pinned Codex harness's tool/Responses format;
	// model placement does not change workspace permissions.
	OSS           bool
	LocalProvider string
	// BaseURL selects a custom Responses-compatible model endpoint. Chat-only
	// servers are not compatible with the pinned Codex harness. Inference may be
	// remote while workspace tools remain on this execution host.
	BaseURL     string
	Cwd         string
	RunDir      string // explicit private evidence directory; empty keeps the response in memory
	Label       string
	TimeoutMs   int
	SandboxMode string
}

// Result is the parsed outcome of a codex call.
type Result struct {
	ThreadID    string          `json:"threadId,omitempty"`
	OK          bool            `json:"ok"`
	Status      int             `json:"status"`
	TimedOut    bool            `json:"timedOut"`
	RateLimited bool            `json:"rateLimited,omitempty"`
	Text        string          `json:"text"`
	JSON        json.RawMessage `json:"json,omitempty"`
	LogPath     string          `json:"logPath,omitempty"`
	Sandbox     string          `json:"sandbox,omitempty"`
	// Token usage from the turn.completed JSONL event (zero when --json unavailable).
	InputTokens       int `json:"inputTokens,omitempty"`
	CachedInputTokens int `json:"cachedInputTokens,omitempty"`
	OutputTokens      int `json:"outputTokens,omitempty"`
	ReasoningTokens   int `json:"reasoningTokens,omitempty"`
}

// usageSidecar is the file written to <RunDir>/<Label>.usage.json.
type usageSidecar struct {
	ThreadID          string `json:"threadId,omitempty"`
	Label             string `json:"label"`
	Sandbox           string `json:"sandbox"`
	InputTokens       int    `json:"inputTokens"`
	CachedInputTokens int    `json:"cachedInputTokens"`
	OutputTokens      int    `json:"outputTokens"`
	ReasoningTokens   int    `json:"reasoningTokens"`
}

// parseUsage scans JSONL stdout for the LAST turn.completed event and
// returns its usage block. It is best-effort; all zeros on failure.
func parseUsage(stdout string) (input, cached, output, reasoning int) {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Usage struct {
				InputTokens           int `json:"input_tokens"`
				CachedInputTokens     int `json:"cached_input_tokens"`
				OutputTokens          int `json:"output_tokens"`
				ReasoningOutputTokens int `json:"reasoning_output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "turn.completed" {
			return ev.Usage.InputTokens, ev.Usage.CachedInputTokens,
				ev.Usage.OutputTokens, ev.Usage.ReasoningOutputTokens
		}
	}
	return 0, 0, 0, 0
}

// isRateLimited detects a codex/account usage-limit or rate-limit error in the
// process output. Such a failure is an INFRASTRUCTURE error, not a quality
// verdict — callers must abort rather than record a (false) failing score.
//
// The match must be SPECIFIC: codex output includes the agent's generated content
// (app code, colors, sizes), so loose tokens like a bare "429" or "rate limit"
// false-positive on normal output and wrongly kill the node. Match only the exact
// error phrasings codex/the API emit.
func isRateLimited(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "hit your usage limit") || // codex: "You've hit your usage limit"
		strings.Contains(l, "usage limit reached") ||
		strings.Contains(l, "rate limit exceeded") ||
		strings.Contains(l, "429 too many requests")
}

// withDefaults supplies conservative defaults for auxiliary SDK conversations.
func (o Options) withDefaults() Options {
	if o.SandboxMode == "" {
		o.SandboxMode = SandboxReadOnly
	}
	if o.TimeoutMs == 0 {
		o.TimeoutMs = int(DefaultTimeout / time.Millisecond)
	}
	if o.Reasoning == "" {
		o.Reasoning = "low"
	}
	return o
}

// Run executes one read-only auxiliary SDK call. Writable work uses RunWorkflow
// through sdkruntime. Evidence files are written only to an explicit RunDir.
func Run(ctx context.Context, o Options) (Result, error) {
	o = o.withDefaults()
	if o.SandboxMode != SandboxReadOnly {
		return Result{}, fmt.Errorf("auxiliary SDK calls are read-only; use a managed SDK task for writable work")
	}
	if strings.ContainsAny(o.Label, "/\\\x00") || o.Label == "." || o.Label == ".." {
		return Result{}, fmt.Errorf("SDK evidence label must be a filename, not a path")
	}
	if o.RunDir != "" {
		info, err := os.Stat(o.RunDir)
		if err != nil || !info.IsDir() {
			return Result{}, fmt.Errorf("SDK evidence directory must already exist")
		}
		if o.Label == "" {
			return Result{}, fmt.Errorf("an explicit SDK evidence directory requires a label")
		}
	}

	ctxBlock := ""
	if len(o.ContextFiles) > 0 {
		ctxBlock = fmt.Sprintf("\n\nCONTEXT FILES — read these first (paths relative to %s):\n", o.Cwd)
		for _, f := range o.ContextFiles {
			ctxBlock += "  - " + f + "\n"
		}
	}
	prompt := o.Instruction + ctxBlock

	worker, err := sdkWorkerPath()
	if err != nil {
		return Result{}, err
	}
	request := struct {
		Options
		Prompt string
	}{o, prompt}
	payload, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	res := ExecAsync(ctx, "node", []string{worker}, ExecOpts{TimeoutMs: o.TimeoutMs, Input: string(payload)})

	text, threadID, completed := sdkResult(res.Stdout)
	logPath := ""
	if o.RunDir != "" {
		logPath = filepath.Join(o.RunDir, o.Label+".log")
		logBody := fmt.Sprintf("# SDK auxiliary call; sandbox: %s\n\n=== PROMPT ===\n%s\n\n=== STDOUT ===\n%s\n\n=== STDERR ===\n%s", o.SandboxMode, prompt, res.Stdout, res.Stderr)
		if err := os.WriteFile(logPath, []byte(logBody), 0600); err != nil {
			return Result{}, fmt.Errorf("write SDK log: %w", err)
		}
		if completed {
			if err := os.WriteFile(filepath.Join(o.RunDir, o.Label+".last.txt"), []byte(text), 0600); err != nil {
				return Result{}, err
			}
		}
	}

	parsed := extractJSON(text)
	// Only a completed worker can publish a successful result.
	rateLimited := isRateLimited(res.Stdout + " " + res.Stderr + " " + text)
	ok := completed && !rateLimited && res.Status == 0 && !res.TimedOut

	// Return SDK usage directly and, for an explicit evidence destination only,
	// retain the same usage as a diagnostic file.
	inputTok, cachedTok, outputTok, reasoningTok := parseUsage(res.Stdout)
	if o.RunDir != "" && o.Label != "" {
		sidecar := usageSidecar{
			ThreadID:          threadID,
			Label:             o.Label,
			Sandbox:           o.SandboxMode,
			InputTokens:       inputTok,
			CachedInputTokens: cachedTok,
			OutputTokens:      outputTok,
			ReasoningTokens:   reasoningTok,
		}
		if b, err := json.Marshal(sidecar); err == nil {
			if err := os.WriteFile(filepath.Join(o.RunDir, o.Label+".usage.json"), b, 0600); err != nil {
				return Result{}, fmt.Errorf("write SDK usage: %w", err)
			}
		}
	}

	return Result{
		OK: ok, Status: res.Status, TimedOut: res.TimedOut, RateLimited: rateLimited,
		Text: text, JSON: parsed, LogPath: logPath, Sandbox: o.SandboxMode, ThreadID: threadID,
		InputTokens: inputTok, CachedInputTokens: cachedTok,
		OutputTokens: outputTok, ReasoningTokens: reasoningTok,
	}, nil
}

// extractJSON returns the first parseable JSON object/array in text, or nil.
func extractJSON(text string) json.RawMessage {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	first := strings.IndexAny(trimmed, "[{")
	last := strings.LastIndexAny(trimmed, "]}")
	if first >= 0 && last > first {
		cand := trimmed[first : last+1]
		if json.Valid([]byte(cand)) {
			return json.RawMessage(cand)
		}
	}
	return nil
}

// SDKDirectory resolves application-owned SDK code independently of a workflow's
// working directory, using the same installation as workflow validation.
func SDKDirectory() (string, error) {
	worker, err := sdkWorkerPath()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Dir(worker))
}

// sdkWorkerPath resolves the installed SDK module independently of node cwd.
func sdkWorkerPath() (string, error) {
	root, err := agentchecks.RuntimeDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "src", "worker.mjs"), nil
}

func sdkResult(output string) (text, threadID string, completed bool) {
	for _, line := range strings.Split(output, "\n") {
		var event struct {
			Type            string `json:"type"`
			Text            string `json:"text"`
			ThreadID        string `json:"threadId"`
			StartedThreadID string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Type == "thread.started" {
			threadID = event.StartedThreadID
		}
		if event.Type == "hyperplane.result" {
			text, threadID, completed = event.Text, event.ThreadID, true
		}
	}
	return
}
