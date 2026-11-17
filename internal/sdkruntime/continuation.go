package sdkruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

type ContinuationReview struct {
	Eligible           bool                           `json:"eligible"`
	ReviewToken        string                         `json:"reviewToken,omitempty"`
	Reason             string                         `json:"reason,omitempty"`
	ContinuedRunID     string                         `json:"continuedRunId,omitempty"`
	Reused             []agentchecks.ContinuationStep `json:"reused,omitempty"`
	Restart            *agentchecks.ContinuationStep  `json:"restart"`
	RenewApprovals     []agentchecks.ContinuationStep `json:"renewApprovals,omitempty"`
	RuntimeFingerprint string                         `json:"runtimeFingerprint,omitempty"`
	UsageNote          string                         `json:"usageNote,omitempty"`
}
type continuationSource struct {
	run        store.Run
	request    codex.WorkflowRequest
	checkpoint json.RawMessage
	settings   json.RawMessage
	workspace  string
	revision   string
	plan       agentchecks.ContinuationPlan
}

// ReviewContinuation reads the current stopped receipt and verifies retained
// evidence. The review authorizes no new work and never infers an SDK turn resume.
func ReviewContinuation(ctx context.Context, st *store.Store, runsDir, sourceID string) (ContinuationReview, error) {
	review, _, err := reviewContinuation(ctx, st, runsDir, sourceID)
	return review, err
}
func reviewContinuation(ctx context.Context, st *store.Store, runsDir, sourceID string) (ContinuationReview, *continuationSource, error) {
	var review ContinuationReview
	if sourceID == "" || sourceID == "." || sourceID == ".." || strings.ContainsAny(sourceID, "/\\\x00") {
		return review, nil, store.ErrNotFound
	}
	run, err := st.GetRun(ctx, sourceID)
	if err != nil {
		return review, nil, err
	}
	var document struct {
		SDK          json.RawMessage `json:"sdk"`
		Continuation struct {
			RunID       string `json:"runId"`
			ReviewToken string `json:"reviewToken"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal(run.Doc, &document); err != nil {
		return review, nil, err
	}
	if document.Continuation.RunID != "" {
		review.ContinuedRunID = document.Continuation.RunID
		review.Reason = "This run already has a continuation."
		return review, nil, nil
	}
	if run.State != "interrupted" {
		review.Reason = "Only stopped workflows can continue. Use this setup again to start fresh."
		return review, nil, nil
	}
	var launch struct {
		Runtime string                `json:"runtime"`
		Request codex.WorkflowRequest `json:"request"`
	}
	if err := json.Unmarshal(run.Options, &launch); err != nil {
		return review, nil, err
	}
	if launch.Runtime != "codex-sdk" || launch.Request.Workflow != "node-workflow" || len(launch.Request.Nodes) == 0 {
		review.Reason = "Continue is available for stopped node workflows. Use this setup again for a project task."
		return review, nil, nil
	}
	request := launch.Request
	for _, node := range request.Nodes {
		if node.DependsOn != nil {
			review.Reason = "Continuation is not yet available for workflows with explicit dependencies. Use this setup again to start fresh."
			return review, nil, nil
		}
	}
	if request.RuntimeFingerprint != agentchecks.RuntimeFingerprint() {
		review.Reason = "This app's runtime was updated after this run, so it cannot be continued. Start a new run with the same setup; finished steps are not reused."
		return review, nil, nil
	}
	base, err := filepath.Abs(runsDir)
	if err != nil {
		return review, nil, err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return review, nil, err
	}
	workspace := filepath.Join(base, sourceID, "nodes", "delivery", "OUTPUTS")
	actual, err := filepath.EvalSymlinks(workspace)
	if err != nil || actual != workspace || request.Workspace != workspace {
		review.Reason = "The original managed output folder is missing or has moved."
		return review, nil, nil
	}
	settings, err := json.Marshal(map[string]any{"instruction": request.Instruction, "nodes": request.Nodes, "model": request.Model, "maxTokens": request.MaxTokens, "autoRepair": request.AutoRepair})
	if err != nil {
		return review, nil, err
	}
	plan, err := agentchecks.ReviewContinuation(ctx, settings, document.SDK, workspace)
	if errors.Is(err, agentchecks.ErrContinuationBlocked) {
		review.Reason = err.Error()
		return review, nil, nil
	}
	if err != nil {
		return review, nil, err
	}
	revision, err := store.SDKContinuationRevision(run)
	if err != nil {
		return review, nil, err
	}
	review = ContinuationReview{Eligible: true, ReviewToken: revision, Reused: plan.Reused, Restart: plan.Restart, RenewApprovals: plan.RenewApprovals, RuntimeFingerprint: plan.RuntimeFingerprint, UsageNote: "Recorded usage carries forward; the original budget does not reset. An interrupted turn may have unreported usage."}
	return review, &continuationSource{run: run, request: request, checkpoint: document.SDK, settings: settings, workspace: workspace, revision: revision, plan: plan}, nil
}

// RunContinuation preserves the source and uses a new managed run. Review and
// claim are rechecked before startup; no client-supplied nodes or restart index
// are accepted. The SDK repeats approval steps with the successor's fresh IDs.
func RunContinuation(ctx context.Context, st *store.Store, opts Options, sourceID, token string) (result Result, runErr error) {
	delegated := false
	defer func() {
		if !delegated && opts.Started != nil {
			opts.Started <- runErr
		}
	}()
	if token == "" {
		return result, store.ErrContinuationChanged
	}
	run, err := st.GetRun(ctx, sourceID)
	if err != nil {
		return result, err
	}
	var existing struct {
		Continuation struct {
			RunID       string `json:"runId"`
			ReviewToken string `json:"reviewToken"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal(run.Doc, &existing); err != nil {
		return result, err
	}
	if existing.Continuation.RunID != "" {
		if existing.Continuation.ReviewToken == token {
			return result, &store.SDKContinuationExists{RunID: existing.Continuation.RunID}
		}
		return result, store.ErrContinuationChanged
	}
	review, source, err := reviewContinuation(ctx, st, opts.RunsDir, sourceID)
	if err != nil {
		return result, err
	}
	if !review.Eligible || source == nil || review.ReviewToken != token {
		return result, fmt.Errorf("%w: %s", store.ErrContinuationChanged, review.Reason)
	}
	opts.continuation = source
	opts.Project = source.run.Project
	opts.Goal = source.run.Goal
	opts.TokenBudget = source.request.MaxTokens
	opts.AutoRepair = source.request.AutoRepair
	request := source.request
	request.Workspace = ""
	request.Continuation = &codex.WorkflowContinuation{Checkpoint: source.checkpoint}
	artifacts := map[string]string{}
	for _, node := range request.Nodes {
		artifacts[node.ID+".md"] = "text/markdown"
	}
	delegated = true
	return runSDK(ctx, st, opts, request, runMetadata{Type: source.run.Type, RiskLevel: source.run.RiskLevel}, artifacts)
}

func removeUnstartedContinuation(path string) {
	// This is a fresh, exclusively created destination, never the source workspace.
	_ = os.RemoveAll(path)
}
