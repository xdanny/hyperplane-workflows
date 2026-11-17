package sdkruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/contract"
	"github.com/xdanny/hyperplane-workflows/internal/events"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

// RunAppIdeator retains run ownership and evidence while the SDK owns all model
// turns. There is no DAG scheduling, artifact handoff or child repair run here.
func RunAppIdeator(ctx context.Context, st *store.Store, opts Options) (result Result, runErr error) {
	started := false
	defer func() {
		if !started && opts.Started != nil {
			opts.Started <- runErr
		}
	}()
	for key := range opts.Inputs {
		if key != "domain" {
			return result, fmt.Errorf("unknown ideator input %q", key)
		}
	}
	domain := strings.TrimSpace(opts.Inputs["domain"])
	if domain == "" {
		domain = "offline tools for weekend cyclists"
	}
	if len(domain) > 4000 {
		return result, fmt.Errorf("ideator domain exceeds 4000 bytes")
	}
	started = true
	opts.Inputs = map[string]string{"domain": domain}
	return runSDK(ctx, st, opts, codex.WorkflowRequest{Workflow: "app-ideator", Domain: domain}, runMetadata{Type: "docs-plans", RiskLevel: "low"}, map[string]string{"ideas.json": "application/json", "brief.md": "text/markdown"})
}

// RunCheckedTask executes one explicitly requested project task in the SDK.
// The caller may supply a trusted extra check; no model composes a graph.
func RunCheckedTask(ctx context.Context, st *store.Store, opts Options, instruction string, check []string) (result Result, runErr error) {
	started := false
	defer func() {
		if !started && opts.Started != nil {
			opts.Started <- runErr
		}
	}()
	if err := sdkworkflow.Validate(ctx, instruction, []sdkworkflow.Node{{ID: "task", Kind: "task", Title: "Project task", Instruction: instruction, Check: check}}); err != nil {
		return Result{}, err
	}
	if opts.TargetRoot == "" {
		return Result{}, fmt.Errorf("task target is required")
	}
	workspace, err := filepath.Abs(opts.TargetRoot)
	if err != nil {
		return Result{}, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("task target must be a directory")
	}
	release, err := lockTaskWorkspace(workspace)
	if err != nil {
		return Result{}, err
	}
	defer release()
	started = true
	return runSDK(ctx, st, opts, codex.WorkflowRequest{Workflow: "checked-task", Workspace: workspace, Instruction: instruction, Check: check}, runMetadata{Type: "sdk-task"}, nil)
}

// RunNodeWorkflow runs a reviewed sequence directly in the SDK, in a fresh
// managed folder. Each output is a typed Markdown artifact, not a graph node.
func RunNodeWorkflow(ctx context.Context, st *store.Store, opts Options, instruction string, nodes []sdkworkflow.Node) (result Result, runErr error) {
	if err := sdkworkflow.Validate(ctx, instruction, nodes); err != nil {
		if opts.Started != nil {
			opts.Started <- err
		}
		return result, err
	}
	artifacts := map[string]string{}
	for _, node := range nodes {
		artifacts[node.ID+".md"] = "text/markdown"
	}
	return runSDK(ctx, st, opts, codex.WorkflowRequest{Workflow: "node-workflow", Instruction: instruction, Nodes: nodes}, runMetadata{Type: "docs-plans"}, artifacts)
}

func runSDK(ctx context.Context, st *store.Store, opts Options, request codex.WorkflowRequest, metadata runMetadata, artifacts map[string]string) (result Result, runErr error) {
	started := false
	defer func() {
		if !started && opts.Started != nil {
			opts.Started <- runErr
		}
	}()
	// Check the target boundary without creating directories or consulting storage.
	// Resolve the existing ancestor when the configured runs folder is still absent.
	runsDir, err := canonicalFutureDirectory(opts.RunsDir)
	if err != nil {
		return result, err
	}
	if request.Workspace != "" {
		for _, pair := range [][2]string{{request.Workspace, runsDir}, {runsDir, request.Workspace}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || filepath.IsLocal(rel) {
				return result, fmt.Errorf("task target must be disjoint from run records")
			}
		}
	}
	// Resolve only source metadata before creating any run workspace. Saved
	// settings never replace the submitted request, even after a revision change.
	var projectIdentity store.JSON
	if opts.continuation != nil {
		source := opts.continuation.run
		var err error
		projectIdentity, err = st.ProjectWorkflowIdentity(ctx, source.Options, source.DAG, source.Goal, source.Doc)
		if err != nil {
			return result, err
		}
	} else {
		request.AutoRepair = opts.AutoRepair
		request.Slots, request.SlotLabels = opts.Slots, opts.SlotLabels
		raw, err := json.Marshal(request)
		if err != nil {
			return result, err
		}
		projectIdentity, err = st.PrepareProjectWorkflow(ctx, raw, opts.SourceWorkflowID, opts.WorkflowName, opts.Goal)
		if err != nil {
			return result, err
		}
		// The saved revision this definition was read from is provenance for the
		// grouping identity, which the SQL projection owns. Recording it here
		// does not claim the saved revision executed: the submitted definition
		// did, and an edited one carries no revision at all.
		if opts.SourceRevision > 0 {
			projectIdentity, err = withIdentityField(projectIdentity, "sourceRevision", opts.SourceRevision)
			if err != nil {
				return result, err
			}
		}
		// The sign-off policy is part of the terms this run was launched under,
		// so it is frozen here beside the definition. Editing the saved workflow
		// afterwards changes the next run's terms, never this one's.
		if opts.SignOff != "" {
			projectIdentity, err = withIdentityField(projectIdentity, "signOff", opts.SignOff)
			if err != nil {
				return result, err
			}
		}
	}
	if opts.RunID == "" {
		opts.RunID = NewRunID(request.Workflow)
	}
	if opts.RunID == "." || opts.RunID == ".." || strings.ContainsAny(opts.RunID, "/\\\x00") {
		return result, fmt.Errorf("invalid run id")
	}
	if err := os.MkdirAll(runsDir, 0755); err != nil {
		return result, err
	}
	runsDir, err = filepath.EvalSymlinks(runsDir)
	if err != nil {
		return result, err
	}
	runDir := filepath.Join(runsDir, opts.RunID)
	if err := os.Mkdir(runDir, 0700); err != nil {
		return result, err
	}
	continuationMayBeCommitted := false
	if opts.continuation != nil {
		defer func() {
			if !continuationMayBeCommitted {
				removeUnstartedContinuation(runDir)
			}
		}()
	}
	workspace := request.Workspace
	if workspace == "" {
		workspace = filepath.Join(runDir, "nodes", "delivery", "OUTPUTS")
		if err := os.MkdirAll(workspace, 0700); err != nil {
			return result, err
		}
	} else {
		// The model may write its project, never its own checkpoint journal.
		for _, pair := range [][2]string{{workspace, runsDir}, {runsDir, workspace}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || filepath.IsLocal(rel) {
				return result, fmt.Errorf("task target must be disjoint from run records")
			}
		}
	}
	model := os.Getenv("HYPERPLANE_MODEL")
	if model == "" {
		model = "gpt-6-astra"
	}
	budget := opts.TokenBudget
	if budget <= 0 {
		budget = tokenBudgetFromEnv()
	}
	if budget <= 0 {
		budget = 300000
		if len(request.Nodes) > 1 {
			budget *= len(request.Nodes)
		}
	}
	if opts.continuation != nil {
		model = opts.continuation.request.Model
	}
	request.Workspace, request.Model, request.AutoRepair, request.MaxTokens = workspace, model, opts.AutoRepair, budget
	request.RuntimeFingerprint = agentchecks.RuntimeFingerprint()
	owner, err := st.OwnSDKRun(ctx, opts.RunID)
	if err != nil {
		return result, err
	}
	defer owner.Close()
	ctx = owner.Context
	launchFields := map[string]any{"request": request, "projectWorkflow": projectIdentity, "runtime": "codex-sdk", "workflowVersion": 1, "model": model,
		"ownership": store.SDKOwnership, "ownerSession": owner.SessionID(), "inputs": opts.Inputs, "autoRepair": opts.AutoRepair, "maxTokens": budget}
	if opts.TemplateID != "" {
		launchFields["templateId"] = opts.TemplateID
	}
	if opts.ticket != nil {
		launchFields["ticketId"] = opts.ticket.Ticket.ID
		launchFields["laneId"] = opts.ticket.Lane.ID
	}
	// The first committed record already describes the workflow a person
	// launched: its declared steps, each still pending. Without them a reader
	// polling between admission and the worker's first checkpoint sees a run
	// with no steps at all, which reads as "nothing is happening" for as long
	// as the SDK process takes to start.
	starting := map[string]any{"phase": "starting", "runtimeFingerprint": request.RuntimeFingerprint, "runtimeFingerprintVersion": agentchecks.RuntimeFingerprintVersion}
	if request.Workflow == "node-workflow" {
		starting["nodes"] = declaredSteps(request.Nodes)
	}
	latest, _ := json.Marshal(starting)
	var releaseSource func()
	if source := opts.continuation; source != nil {
		releaseSource, err = owner.HoldContinuationSource(ctx, source.run.RunID)
		if err != nil {
			return result, err
		}
		defer releaseSource()
		plan, err := agentchecks.PrepareContinuation(ctx, source.settings, source.checkpoint, source.workspace, workspace)
		if err != nil {
			return result, err
		}
		if plan.DefinitionFingerprint != source.plan.DefinitionFingerprint {
			return result, store.ErrContinuationChanged
		}
		if len(plan.Checkpoint) == 0 {
			return result, fmt.Errorf("SDK continuation did not provide a prepared checkpoint")
		}
		request.Continuation = &codex.WorkflowContinuation{Checkpoint: plan.Checkpoint}
		launchFields["request"] = request
		launchFields["continuation"] = map[string]any{"sourceRunId": source.run.RunID, "rootRunId": store.SDKDeliveryRoot(source.run), "reviewToken": source.revision, "sourceRequest": source.request, "sourceCheckpoint": source.checkpoint, "definitionFingerprint": plan.DefinitionFingerprint, "usageIsLowerBound": true}
		var initial map[string]any
		if err := json.Unmarshal(plan.Checkpoint, &initial); err != nil {
			return result, err
		}
		initial["phase"] = "starting"
		latest, err = json.Marshal(initial)
		if err != nil {
			return result, err
		}
	}
	launch, _ := json.Marshal(launchFields)
	row := store.Run{RunID: opts.RunID, DAG: request.Workflow, Goal: opts.Goal,
		Project: opts.Project, State: "running", Mode: "run", Type: metadata.Type, RiskLevel: metadata.RiskLevel,
		StartedAt: time.Now(), OwnerPID: os.Getpid(), Options: launch}
	if source := opts.continuation; source != nil {
		row.ParentRun = source.run.RunID
		event, err := owner.StartContinuation(ctx, source.run.RunID, source.revision, row, latest)
		if err != nil {
			// Losing COMMIT's response does not prove rollback. Keep the copied
			// evidence so a durable child can be recovered after ownership ends.
			continuationMayBeCommitted = errors.Is(err, store.ErrContinuationCommitUnknown)
			return result, err
		}
		continuationMayBeCommitted = true
		latest = append(json.RawMessage(nil), event.Data...)
		releaseSource()
		if opts.Bus != nil {
			opts.Bus.Publish(events.Event{RunID: opts.RunID, Seq: event.Seq, Type: "sdk.checkpoint", Node: "delivery", Level: "info", Data: event.Data})
		}
	} else if opts.ticket != nil {
		event, err := owner.StartTicket(ctx, *opts.ticket, row, latest)
		if err != nil {
			return result, err
		}
		latest = event.Data
	} else if err := st.CreateRun(ctx, row); err != nil {
		return result, err
	}
	result.RunID = opts.RunID
	save := func(data json.RawMessage, terminal string, output json.RawMessage) (string, error) {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		state, event, err := owner.SaveCheckpoint(writeCtx, opts.RunID, opts.Project, os.Getpid(), data, terminal, output)
		if err == nil {
			latest = append(json.RawMessage(nil), event.Data...)
			if opts.Bus != nil {
				kind := "sdk.checkpoint"
				if terminal != "" {
					kind = "run.done"
				}
				opts.Bus.Publish(events.Event{RunID: opts.RunID, Seq: event.Seq, Type: kind, Node: "delivery", Level: "info", Data: event.Data})
			}
		}
		return state, err
	}
	var initialErr error
	if opts.continuation == nil && opts.ticket == nil {
		_, initialErr = save(latest, "", nil)
	}
	if err := initialErr; err != nil {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		failed, _ := json.Marshal(map[string]any{"phase": "failed", "passed": false, "error": err.Error(), "runtimeFingerprint": request.RuntimeFingerprint, "runtimeFingerprintVersion": agentchecks.RuntimeFingerprintVersion})
		_, _, _ = owner.SaveCheckpoint(writeCtx, opts.RunID, opts.Project, os.Getpid(), failed, "failed", nil)
		// If the owner connection itself failed, only abandoned-run recovery may
		// finalize this record. A concurrent cancellation remains authoritative.
		if owner.Context.Err() != nil {
			_, _ = st.ReconcileOrphans(writeCtx)
		}
		return result, err
	}
	if opts.Started != nil {
		opts.Started <- nil
	}
	started = true
	receipt, execErr := codex.RunWorkflow(ctx, request,
		filepath.Join(runDir, "delivery.log"), codex.WorkflowHooks{Persist: func(data json.RawMessage) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := save(data, "", nil)
			return err
		}, Approve: func(waitCtx context.Context, state json.RawMessage) (string, error) {
			return awaitSDKApproval(waitCtx, st, opts.RunID, request, state, func(data json.RawMessage) error { _, err := save(data, "", nil); return err })
		}})
	state := "failed"
	if execErr == nil && receipt.Passed {
		state = "passed"
	}
	if ctx.Err() != nil {
		state = "interrupted"
	}
	envelope := contract.Envelope{OK: state == "passed", Outputs: map[string]contract.Value{},
		Verdict:    &contract.Verdict{Pass: state == "passed", Reasons: strings.Join(receipt.Findings, "; ")},
		Provenance: contract.Provenance{Run: opts.RunID, Node: "delivery", Engine: "codex-sdk", Model: model}}
	if state == "passed" {
		if request.Workflow == "node-workflow" {
			raw, _ := json.Marshal(receipt)
			err := verifySDKDocuments(ctx, request, raw, "")
			var files []map[string]string
			if err == nil {
				files, err = sdkWorkFiles(ctx, request, raw, "", opts.RunID)
			}
			if err != nil {
				execErr, state = err, "failed"
			} else {
				for _, file := range files {
					media := mime.TypeByExtension(filepath.Ext(file["file"]))
					if media == "" {
						media = "application/octet-stream"
					}
					bytes, _ := strconv.ParseInt(file["bytes"], 10, 64)
					envelope.Outputs[file["file"]] = contract.ArtifactValue(contract.ArtifactRef{Artifact: "nodes/delivery/OUTPUTS/" + file["file"], Media: media, Bytes: bytes, SHA256: file["sha256"]})
				}
			}
		}
		for name, media := range artifacts {
			data, err := readSDKArtifact(filepath.Join(workspace, name))
			if err != nil {
				execErr = err
				state = "failed"
				break
			}
			envelope.Outputs[strings.TrimSuffix(name, filepath.Ext(name))] = contract.ArtifactValue(contract.ArtifactRef{Artifact: "nodes/delivery/OUTPUTS/" + name, Media: media, Bytes: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
		}
	}
	if ctx.Err() != nil {
		state, execErr = "interrupted", context.Cause(ctx)
	}
	var final map[string]any
	_ = json.Unmarshal(latest, &final)
	if execErr == nil {
		data, _ := json.Marshal(receipt)
		_ = json.Unmarshal(data, &final)
	}
	final["phase"] = state
	final["passed"] = state == "passed"
	if execErr != nil {
		final["error"] = execErr.Error()
		envelope.Verdict.Reasons = execErr.Error()
	}
	envelope.OK, envelope.Verdict.Pass = state == "passed", state == "passed"
	data, _ := json.Marshal(final)
	output, _ := json.Marshal(envelope)
	result.State, runErr = save(data, state, output)
	if runErr == nil && result.State == "passed" {
		// The committed record, not the worker's report, decides this.
		recordAutomaticAcceptance(ctx, st, opts.RunID)
	}
	if runErr != nil && ctx.Err() != nil {
		// A lost ownership connection cannot publish even its terminal checkpoint.
		// Recovery can only interrupt after acquiring the abandoned lock; it never
		// accepts a late worker result or replays work.
		recoveryCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if _, err := st.ReconcileOrphans(recoveryCtx); err == nil {
			if saved, err := st.GetRun(recoveryCtx, opts.RunID); err == nil && saved.State == "interrupted" {
				result.State, runErr = "interrupted", nil
			}
		}
	}
	return result, runErr
}

func readSDKArtifact(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 1_000_000 {
		return nil, fmt.Errorf("invalid SDK artifact")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1_000_001))
	if len(data) > 1_000_000 {
		return nil, fmt.Errorf("SDK artifact exceeds 1 MB")
	}
	return data, err
}

// Native advisory directory locks serialize overlapping SDK project writers
// across API and CLI processes on this host. Shared ancestor locks allow
// disjoint sibling targets; the target itself is exclusive. The OS releases
// these descriptors on process exit. Other applications must cooperate.
func lockTaskWorkspace(workspace string) (func(), error) {
	var held []*os.File
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i].Close()
		}
	}
	for path := workspace; ; path = filepath.Dir(path) {
		file, err := os.Open(path)
		if err != nil {
			release()
			return nil, err
		}
		held = append(held, file)
		mode := syscall.LOCK_SH
		if path == workspace {
			mode = syscall.LOCK_EX
		}
		if err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB); err != nil {
			release()
			return nil, fmt.Errorf("%w: %w", ErrWorkspaceBusy, err)
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return release, nil
}

// canonicalFutureDirectory resolves symlinks without creating a missing path.
func canonicalFutureDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			rel, err := filepath.Rel(ancestor, absolute)
			return filepath.Join(resolved, rel), err
		}
		if !os.IsNotExist(err) || filepath.Dir(ancestor) == ancestor {
			return "", err
		}
		ancestor = filepath.Dir(ancestor)
	}
}

// withIdentityField adds one launch-time field to the organizational identity
// without recomputing it. The projection keeps ownership of key and name.
func withIdentityField(identity store.JSON, field string, value any) (store.JSON, error) {
	fields := map[string]any{}
	if len(identity) > 0 {
		if err := json.Unmarshal(identity, &fields); err != nil {
			return identity, err
		}
	}
	fields[field] = value
	raw, err := json.Marshal(fields)
	if err != nil {
		return identity, err
	}
	return store.JSON(raw), nil
}

// declaredSteps projects the frozen request into the pending step records the
// SDK worker will replace on its first checkpoint. It reports only what was
// declared: no step is claimed to have started, and an extra acceptance check
// is distinguished from work that requested none.
func declaredSteps(nodes []sdkworkflow.Node) []map[string]any {
	steps := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		step := map[string]any{"id": node.ID, "title": node.Title, "phase": "pending"}
		if node.Kind == "task" {
			verification := "not_requested"
			if len(node.Check) > 0 || node.Acceptance != "" {
				verification = "pending"
			}
			step["verification"] = verification
		}
		steps = append(steps, step)
	}
	return steps
}
