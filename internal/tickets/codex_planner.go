// codex_planner.go — production PlannerFunc backed by the codex engine adapter.
package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
)

// NewCodexPlanner returns a PlannerFunc that calls codex to produce a ticket
// plan.  engineName selects "codex" (cloud) or "codex-local" / "codex-oss"
// (local model) for this read-only planning conversation.
func NewCodexPlanner(engineName string) PlannerFunc {
	oss := strings.HasPrefix(engineName, "codex-local") ||
		strings.HasPrefix(engineName, "codex-oss")

	return func(ctx context.Context, prompt string, schema interface{}) ([]PlanTicket, error) {
		workspace, err := os.MkdirTemp("", "hyperplane-ticket-plan-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(workspace)
		opts := codex.Options{
			Cwd: workspace, RunDir: workspace, Label: "plan",
			Instruction: prompt,
			SandboxMode: codex.SandboxReadOnly,
			Reasoning:   "high",
			OSS:         oss,
		}

		result, err := codex.Run(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("codex planner: %w", err)
		}
		if result.RateLimited {
			return nil, fmt.Errorf("codex planner: rate limited")
		}
		if !result.OK {
			return nil, fmt.Errorf("codex planner: exit status %d: %s", result.Status, result.Text)
		}

		// codex.Run extracts the first JSON object/array from the output into
		// result.JSON.  Try that first; fall back to scanning result.Text.
		var raw []byte
		if len(result.JSON) > 0 && json.Valid(result.JSON) {
			raw = result.JSON
		} else {
			// Try to extract a JSON array from the text.
			text := strings.TrimSpace(result.Text)
			first := strings.Index(text, "[")
			last := strings.LastIndex(text, "]")
			if first >= 0 && last > first {
				raw = []byte(text[first : last+1])
			}
		}

		if len(raw) == 0 {
			return nil, fmt.Errorf("codex planner: no JSON array found in output")
		}

		var tickets []PlanTicket
		if err := json.Unmarshal(raw, &tickets); err != nil {
			return nil, fmt.Errorf("codex planner: unmarshal tickets: %w", err)
		}
		return tickets, nil
	}
}
