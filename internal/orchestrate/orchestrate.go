// Package orchestrate runs each backlog item as one checked Codex SDK session.
package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

type BacklogItem struct {
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Check  []string `json:"check,omitempty"`
}

type OrchestrateItemResult struct {
	Title string `json:"title"`
	RunID string `json:"runId"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type OrchestrateConfig struct {
	BacklogItems []BacklogItem
	MaxItems     int
	TargetRoot   string
	Engine       string // only Codex SDK; old model routes are explicitly refused
	DryRun       bool
	StopOnFail   bool
	AutoRepair   bool
	RunsDir      string
	Project      string
	// Progress must commit before the next item starts.
	Progress func(OrchestrateResult) error
	// Execute allows the application runner to own child cancellation and shutdown.
	Execute func(context.Context, sdkruntime.Options, string, []string) (sdkruntime.Result, error)
}

type OrchestrateResult struct {
	Items  []OrchestrateItemResult `json:"items"`
	Done   int                     `json:"done"`
	Failed int                     `json:"failed"`
}

// Validate checks the whole selected backlog before spending any model calls.
func Validate(ctx context.Context, cfg OrchestrateConfig) error {
	if cfg.Engine != "" && cfg.Engine != "codex" && cfg.Engine != "codex-sdk" {
		return fmt.Errorf("backlog execution now uses Codex SDK; remove the legacy engine override")
	}
	if len(cfg.BacklogItems) == 0 {
		return fmt.Errorf("backlog must not be empty")
	}
	for i, item := range selected(cfg) {
		if strings.TrimSpace(item.Title) == "" {
			return fmt.Errorf("backlog item %d has no title", i+1)
		}
		if !cfg.DryRun && (len(item.Check) == 0 || item.Check[0] == "") {
			return fmt.Errorf("backlog item %d needs check: an explicit acceptance command argument array", i+1)
		}
		instruction := item.Title + "\n\n" + item.Detail
		if err := sdkworkflow.Validate(ctx, instruction, []sdkworkflow.Node{{ID: "task", Kind: "task", Title: "Project task", Instruction: instruction, Check: item.Check}}); err != nil {
			return fmt.Errorf("backlog item %d: %w", i+1, err)
		}
	}
	return nil
}

func selected(cfg OrchestrateConfig) []BacklogItem {
	if cfg.MaxItems > 0 && cfg.MaxItems < len(cfg.BacklogItems) {
		return cfg.BacklogItems[:cfg.MaxItems]
	}
	return cfg.BacklogItems
}

// Orchestrate retains ordering and stop-on-failure. The SDK owns each task's
// model/tool turns and bounded correction; no composition or graph run occurs.
func Orchestrate(ctx context.Context, st *store.Store, cfg OrchestrateConfig) (OrchestrateResult, error) {
	result := OrchestrateResult{Items: []OrchestrateItemResult{}}
	if err := Validate(ctx, cfg); err != nil {
		return result, err
	}
	for _, item := range selected(cfg) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		outcome := OrchestrateItemResult{Title: item.Title, State: "dry-run"}
		if !cfg.DryRun {
			execute := cfg.Execute
			if execute == nil {
				execute = func(ctx context.Context, opts sdkruntime.Options, task string, check []string) (sdkruntime.Result, error) {
					return sdkruntime.RunCheckedTask(ctx, st, opts, task, check)
				}
			}
			run, err := execute(ctx, sdkruntime.Options{
				RunsDir: cfg.RunsDir, TargetRoot: cfg.TargetRoot, Project: cfg.Project,
				Goal: item.Title, AutoRepair: cfg.AutoRepair,
			}, item.Title+"\n\n"+item.Detail, item.Check)
			outcome.RunID, outcome.State = run.RunID, "failed"
			if err != nil {
				outcome.Error = err.Error()
			}
			if run.State == "passed" && err == nil {
				outcome.State = "done"
			}
			if outcome.State == "done" {
				result.Done++
			} else {
				result.Failed++
			}
		}
		result.Items = append(result.Items, outcome)
		if cfg.Progress != nil {
			if err := cfg.Progress(result); err != nil {
				return result, err
			}
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if cfg.StopOnFail && outcome.State == "failed" {
			break
		}
	}
	return result, nil
}

// LoadBacklog reads a JSON file containing an array of BacklogItem objects.
func LoadBacklog(path string) ([]BacklogItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load backlog: %w", err)
	}
	return LoadBacklogFromRunOutput(data)
}

// LoadBacklogFromRunOutput parses a backlog payload produced by a backlog-node.
// Accepts:
//   - []string  — each string becomes a BacklogItem.Title
//   - []{title,detail} — decoded directly as []BacklogItem
func LoadBacklogFromRunOutput(output []byte) ([]BacklogItem, error) {
	if len(output) == 0 {
		return nil, fmt.Errorf("load backlog: empty input")
	}

	// Try []BacklogItem first.
	var items []BacklogItem
	if err := json.Unmarshal(output, &items); err == nil && len(items) > 0 {
		// Validate that at least one item has a non-empty title. If every item
		// has an empty title the bytes were probably an array of strings that
		// JSON decoded successfully as structs with zero values.
		for _, it := range items {
			if it.Title != "" {
				return items, nil
			}
		}
	}

	// Try []string.
	var strs []string
	if err := json.Unmarshal(output, &strs); err == nil {
		items = make([]BacklogItem, len(strs))
		for i, s := range strs {
			items[i] = BacklogItem{Title: s}
		}
		return items, nil
	}

	// Try {items: [...]} wrapper object.
	var wrapper struct {
		Items []BacklogItem `json:"items"`
	}
	if err := json.Unmarshal(output, &wrapper); err == nil && len(wrapper.Items) > 0 {
		return wrapper.Items, nil
	}

	return nil, fmt.Errorf("load backlog: cannot parse input as []string or []BacklogItem")
}
