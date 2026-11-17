package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// ErrInvalidSource distinguishes unusable saved evidence from storage failure.
var ErrInvalidSource = errors.New("invalid SDK source")

// ConfigFromRun reads only this completed run's frozen SDK request and committed
// checkpoint. It never reads output paths, old graph nodes or ancestor records.
func ConfigFromRun(ctx context.Context, st DecomposeStore, runID, targetRoot string) (DecomposeConfig, error) {
	var cfg DecomposeConfig
	if strings.TrimSpace(runID) == "" {
		return cfg, fmt.Errorf("%w: source run ID is required", ErrInvalidSource)
	}
	run, err := st.GetRun(ctx, runID)
	if err != nil {
		return cfg, fmt.Errorf("read source run: %w", err)
	}
	if run.RunID != runID || run.State != "passed" || store.IsRehearsal(run) {
		return cfg, fmt.Errorf("%w: source must be a completed, non-rehearsal SDK run", ErrInvalidSource)
	}
	if len(run.Options) > 1<<20 || len(run.Doc) > 1<<20 {
		return cfg, fmt.Errorf("%w: source SDK record exceeds the size limit", ErrInvalidSource)
	}
	var launch struct {
		Runtime string `json:"runtime"`
		Request struct {
			Workflow    string `json:"workflow"`
			Instruction string `json:"instruction"`
			Nodes       []struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				Instruction string `json:"instruction"`
			} `json:"nodes"`
		} `json:"request"`
	}
	var doc struct {
		SDK struct {
			Phase    string   `json:"phase"`
			Text     string   `json:"text"`
			Findings []string `json:"findings"`
			Nodes    []struct {
				ID       string   `json:"id"`
				Phase    string   `json:"phase"`
				Text     string   `json:"text"`
				Findings []string `json:"findings"`
			} `json:"nodes"`
		} `json:"sdk"`
	}
	if json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" || json.Unmarshal(run.Doc, &doc) != nil || doc.SDK.Phase != "passed" {
		return cfg, fmt.Errorf("%w: source has no completed SDK checkpoint", ErrInvalidSource)
	}
	var evidence strings.Builder
	evidence.WriteString("Recorded completion is context, not independent verification. No output files were opened.\n")
	switch launch.Request.Workflow {
	case "node-workflow":
		if len(launch.Request.Nodes) == 0 || len(launch.Request.Nodes) > 20 || len(doc.SDK.Nodes) != len(launch.Request.Nodes) {
			return cfg, fmt.Errorf("%w: source SDK step identities are incomplete", ErrInvalidSource)
		}
		seen := map[string]bool{}
		for i, step := range launch.Request.Nodes {
			observed := doc.SDK.Nodes[i]
			if step.ID == "" || seen[step.ID] || observed.ID != step.ID || observed.Phase != "passed" {
				return cfg, fmt.Errorf("%w: source SDK step identities or completion do not match", ErrInvalidSource)
			}
			seen[step.ID] = true
			fmt.Fprintf(&evidence, "Step %s (%s)\nRequested: %s\nRecorded text: %s\nRecorded findings: %s\n", sourceExcerpt(step.ID, 80), sourceExcerpt(step.Title, 160), sourceExcerpt(step.Instruction, 600), sourceExcerpt(observed.Text, 1600), sourceExcerpt(strings.Join(observed.Findings, "; "), 600))
		}
	case "checked-task", "app-ideator":
		fmt.Fprintf(&evidence, "Recorded text: %s\nRecorded findings: %s\n", sourceExcerpt(doc.SDK.Text, 12000), sourceExcerpt(strings.Join(doc.SDK.Findings, "; "), 3000))
	default:
		return cfg, fmt.Errorf("%w: source is not a supported SDK workflow", ErrInvalidSource)
	}
	name := sourceExcerpt(run.Goal, 160)
	if name == "" {
		name = runID
	}
	cfg = DecomposeConfig{AppName: name, Concept: sourceExcerpt(launch.Request.Instruction, 4000), Evidence: evidence.String(), TargetRoot: targetRoot, Project: run.Project, OriginRun: runID}
	return cfg, nil
}

func sourceExcerpt(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		return string(runes[:limit]) + "… [excerpt truncated]"
	}
	return string(runes)
}
