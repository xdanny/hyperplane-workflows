package httpapi

import (
	"context"
	"fmt"
	"sync"

	"github.com/xdanny/hyperplane-workflows/internal/events"
	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// runner owns launch, cancellation and shutdown for SDK work, routing live
// events through a shared *events.Bus.
// The bus and store are owned by the Server; runner holds references only.
type runner struct {
	st      *store.Store
	bus     *events.Bus
	runsDir string

	mu      sync.Mutex
	active  map[string]context.CancelFunc // runID -> cancel
	closing bool
	workers sync.WaitGroup
}

func newRunner(st *store.Store, bus *events.Bus, runsDir string) *runner {
	return &runner{
		st:      st,
		bus:     bus,
		runsDir: runsDir,
		active:  make(map[string]context.CancelFunc),
	}
}

// workflowSource is the lineage a launch carries: which saved setup or shipped
// template it came from, which revision was read, and the reusable-input values
// it was given. None of it changes execution — the submitted definition does —
// so a run stays readable as "run #3 of Weekly competitor brief, for week 37".
type workflowSource struct {
	id, name   string
	templateID string
	slots      map[string]string
	// slotLabels are the human labels the saved workflow gave those inputs, so a
	// later reader sees "Week to cover", not "weekToCover".
	slotLabels map[string]string
	// signOff is the saved workflow's sign-off policy at the moment of launch.
	signOff  string
	revision int64
}

func applyWorkflowSource(opts sdkruntime.Options, source []workflowSource) sdkruntime.Options {
	if len(source) > 0 {
		opts.SourceWorkflowID, opts.WorkflowName = source[0].id, source[0].name
		opts.Slots, opts.SourceRevision, opts.TemplateID = source[0].slots, source[0].revision, source[0].templateID
		opts.SlotLabels, opts.SignOff = source[0].slotLabels, source[0].signOff
	}
	return opts
}

func (r *runner) StartAppIdeator(inputs map[string]string, goal, project string, autoRepair bool, source ...workflowSource) (string, error) {
	runID := sdkruntime.NewRunID("app-ideator")
	started := make(chan error, 1)
	if err := r.launch(runID, nil, func(ctx context.Context) {
		_, _ = sdkruntime.RunAppIdeator(ctx, r.st, applyWorkflowSource(sdkruntime.Options{RunID: runID, RunsDir: r.runsDir,
			Inputs: inputs, Goal: goal, Project: project, AutoRepair: autoRepair, Bus: r.bus, Started: started}, source))
	}); err != nil {
		return "", err
	}
	if err := <-started; err != nil {
		return "", err
	}
	return runID, nil
}

func (r *runner) StartCheckedTask(instruction string, check []string, target, project string, autoRepair bool, source ...workflowSource) (string, error) {
	runID := sdkruntime.NewRunID("checked-task")
	started := make(chan error, 1)
	if err := r.launch(runID, nil, func(ctx context.Context) {
		_, _ = sdkruntime.RunCheckedTask(ctx, r.st, applyWorkflowSource(sdkruntime.Options{RunID: runID, RunsDir: r.runsDir,
			TargetRoot: target, Goal: instruction, Project: project, AutoRepair: autoRepair, Bus: r.bus, Started: started}, source), instruction, check)
	}); err != nil {
		return "", err
	}
	if err := <-started; err != nil {
		return "", err
	}
	return runID, nil
}

func (r *runner) StartNodeWorkflow(instruction string, nodes []sdkworkflow.Node, project string, autoRepair bool, source ...workflowSource) (string, error) {
	runID := sdkruntime.NewRunID("node-workflow")
	started := make(chan error, 1)
	if err := r.launch(runID, nil, func(ctx context.Context) {
		_, _ = sdkruntime.RunNodeWorkflow(ctx, r.st, applyWorkflowSource(sdkruntime.Options{RunID: runID, RunsDir: r.runsDir, Goal: instruction, Project: project, AutoRepair: autoRepair, Bus: r.bus, Started: started}, source), instruction, nodes)
	}); err != nil {
		return "", err
	}
	if err := <-started; err != nil {
		return "", err
	}
	return runID, nil
}

// StartContinuation uses the normal managed lifecycle and waits until the
// successor claim and its first checkpoint are committed.
func (r *runner) StartContinuation(sourceID, reviewToken string) (string, error) {
	runID := sdkruntime.NewRunID("continuation")
	started := make(chan error, 1)
	if err := r.launch(runID, nil, func(ctx context.Context) {
		_, _ = sdkruntime.RunContinuation(ctx, r.st, sdkruntime.Options{RunID: runID, RunsDir: r.runsDir, Bus: r.bus, Started: started}, sourceID, reviewToken)
	}); err != nil {
		return "", err
	}
	if err := <-started; err != nil {
		return "", err
	}
	return runID, nil
}

// Every SDK entrypoint shares cancellation, shutdown and startup ownership.
func (r *runner) launch(runID string, parent context.Context, work func(context.Context)) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		cancel()
		return fmt.Errorf("runner is shutting down")
	}
	if _, exists := r.active[runID]; exists {
		r.mu.Unlock()
		cancel()
		return fmt.Errorf("run %q is already active", runID)
	}
	r.workers.Add(1)
	r.active[runID] = cancel
	r.mu.Unlock()
	go func() {
		defer r.workers.Done()
		defer func() { r.mu.Lock(); delete(r.active, runID); r.mu.Unlock(); cancel() }()
		work(ctx)
	}()
	return nil
}

// Cancel requests cancellation of an active run. No-op if the run is not active.
func (r *runner) Cancel(runID string) {
	r.mu.Lock()
	cancel, ok := r.active[runID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
}

// shutdown stops accepting work, cancels active subprocesses and waits for their
// final state writes before the server closes its database.
func (r *runner) shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.closing = true
	for _, cancel := range r.active {
		cancel()
	}
	r.mu.Unlock()
	done := make(chan struct{})
	go func() { r.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunCheckedTask joins a managed child SDK run so batch and per-run cancellation
// share the same ownership, and the batch waits for terminal persistence.
func (r *runner) RunCheckedTask(ctx context.Context, opts sdkruntime.Options, instruction string, check []string) (sdkruntime.Result, error) {
	opts.RunID = sdkruntime.NewRunID("checked-task")
	opts.Bus = r.bus
	type outcome struct {
		result sdkruntime.Result
		err    error
	}
	done := make(chan outcome, 1)
	if err := r.launch(opts.RunID, ctx, func(child context.Context) {
		result, err := sdkruntime.RunCheckedTask(child, r.st, opts, instruction, check)
		done <- outcome{result, err}
	}); err != nil {
		return sdkruntime.Result{}, err
	}
	result := <-done
	return result.result, result.err
}

// StartLaneTicket waits for atomic ticket claim and SDK startup before returning.
func (r *runner) StartLaneTicket(source store.SDKTicketAdmission, target, platformRoot string) (string, error) {
	runID := sdkruntime.NewRunID("ticket")
	started := make(chan error, 1)
	if err := r.launch(runID, nil, func(ctx context.Context) {
		_, _ = sdkruntime.RunTicket(ctx, r.st, sdkruntime.Options{RunID: runID, RunsDir: r.runsDir, TargetRoot: target, Bus: r.bus, Started: started}, source, platformRoot)
	}); err != nil {
		return "", err
	}
	if err := <-started; err != nil {
		return "", err
	}
	return runID, nil
}
