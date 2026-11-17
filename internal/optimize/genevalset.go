// Package optimize — evalset generator.
// GenerateEvalset prompts a model to produce a small, discriminating evalset
// (3–4 cases with harsh criteria) for a skill, then persists it via
// store.UpsertEvalset so optimize/improve can resolve it by skill_ref.
package optimize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// genEvalsetEngine is the subset of an engine needed by GenerateEvalset.
// It lets tests inject a mock without importing eval (which would cycle).
type genEvalsetEngine interface {
	runRaw(ctx context.Context, prompt string) (text string, rawJSON json.RawMessage, err error)
}

// codexGenEngine wraps codex.Run for GenerateEvalset.
type codexGenEngine struct{}

func (e *codexGenEngine) runRaw(ctx context.Context, prompt string) (string, json.RawMessage, error) {
	workspace, err := os.MkdirTemp("", "hyperplane-evalset-proposal-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(workspace)
	res, err := codex.Run(ctx, codex.Options{
		Instruction: prompt,
		Reasoning:   "low",
		SandboxMode: codex.SandboxReadOnly,
		Cwd:         workspace, RunDir: workspace, Label: "evalset",
	})
	if err != nil {
		return "", nil, err
	}
	if res.RateLimited {
		return "", nil, fmt.Errorf("codex rate-limited")
	}
	if !res.OK {
		return "", nil, fmt.Errorf("codex failed (status %d)", res.Status)
	}
	return res.Text, res.JSON, nil
}

// localGenEngine wraps localmodel.Run for GenerateEvalset.
type localGenEngine struct{ model string }

func (e *localGenEngine) runRaw(ctx context.Context, prompt string) (string, json.RawMessage, error) {
	res, err := localmodel.Run(ctx, localmodel.Options{
		Instruction: prompt,
		Model:       e.model,
	})
	if err != nil {
		return "", nil, err
	}
	if !res.OK {
		return "", nil, fmt.Errorf("local model returned no usable output")
	}
	return res.Text, res.JSON, nil
}

// genCaseRaw is the model's raw output shape for one eval case.
type genCaseRaw struct {
	Input    json.RawMessage `json:"input"`
	Criteria string          `json:"criteria"`
	Checks   []struct {
		Kind string `json:"kind"`
	} `json:"checks"`
}

// genEvalsetPrompt returns the prompt sent to the model.
// It embeds the harsh-judge philosophy so generated criteria are
// discriminating, not box-ticking.
func genEvalsetPrompt(skillID, instruction string) string {
	return fmt.Sprintf(`You are designing a HARSH, discriminating evalset for the following skill.

SKILL ID: %s

SKILL INSTRUCTION:
%s

Your task: generate 3–4 representative evaluation cases. Each case tests a
DIFFERENT, realistic task that this skill would be asked to perform.

HARSH CRITERIA PHILOSOPHY — apply this strictly:
- Each criterion must distinguish EXCELLENT output from MEDIOCRE output for
  THIS SPECIFIC SKILL. Generic criteria ("output is non-empty", "response is
  helpful") are worthless — never write them.
- A criterion must demand specificity, depth, and correctness on a particular
  dimension that a weak model would fail. Mediocre outputs that "technically
  mention the required elements but are generic, vague, or thin" should FAIL
  the criterion.
- Phrase criteria as what MUST be present/true in an EXCELLENT response (e.g.
  "must include concrete X, with Y, without Z").
- Penalise: genericness, hand-waving, buzzwords, obvious ideas, named-but-not-
  made-concrete elements, and confident-sounding shallowness.

For each case, also include 1–2 deterministic checks from:
  "json-parse"      — output must be valid JSON
  "text-nonempty"   — output must be non-empty text

Return ONLY a JSON array (no wrapping text, no markdown fences) with this
exact shape per element:
[
  {
    "input": { <a realistic task input object for this skill — specific, not generic> },
    "criteria": "<one HARSH, skill-specific quality criterion — long enough to be unambiguous>",
    "checks": [{"kind": "text-nonempty"}]
  }
]

Produce 3–4 elements. Return ONLY the JSON array.`, skillID, instruction)
}

// parseGenCases extracts []genCaseRaw from a model response.
// It tries res.JSON first (structured path), then scans the text for the
// first JSON array, then tries to parse the text directly as a JSON array.
func parseGenCases(rawJSON json.RawMessage, text string) ([]genCaseRaw, error) {
	// Helper: try to decode bytes as []genCaseRaw.
	tryDecode := func(b []byte) ([]genCaseRaw, bool) {
		var cases []genCaseRaw
		if err := json.Unmarshal(b, &cases); err == nil && len(cases) > 0 {
			return cases, true
		}
		return nil, false
	}

	// 1. Structured JSON output from the model (most reliable).
	if len(rawJSON) > 0 {
		if cases, ok := tryDecode(rawJSON); ok {
			return cases, nil
		}
	}

	// 2. Extract the first JSON array from free text.
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, "[")
	end := strings.LastIndex(trimmed, "]")
	if start >= 0 && end > start {
		cand := trimmed[start : end+1]
		if cases, ok := tryDecode([]byte(cand)); ok {
			return cases, nil
		}
	}

	// 3. Try parsing the whole trimmed text as a JSON array.
	if cases, ok := tryDecode([]byte(trimmed)); ok {
		return cases, nil
	}

	return nil, fmt.Errorf("model response did not contain a parseable JSON array of eval cases; raw text: %.200s", text)
}

// buildEvalset converts raw model output into a store.Evalset.
func buildEvalset(skillID string, rawCases []genCaseRaw) store.Evalset {
	es := store.Evalset{
		ID:       skillID + "-eval",
		Name:     "Auto evalset for " + skillID,
		SkillRef: skillID,
	}

	for i, rc := range rawCases {
		// Ensure Input is valid JSON; default to {} if empty.
		input := rc.Input
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage(`{}`)
		}

		// Build checks; default to text-nonempty if none provided.
		var checks []store.EvalCheckSpec
		for _, ch := range rc.Checks {
			if ch.Kind == "json-parse" || ch.Kind == "text-nonempty" || ch.Kind == "count-min" {
				checks = append(checks, store.EvalCheckSpec{Kind: ch.Kind})
			}
		}
		if len(checks) == 0 {
			checks = []store.EvalCheckSpec{{Kind: "text-nonempty"}}
		}

		criteria := strings.TrimSpace(rc.Criteria)
		if criteria == "" {
			criteria = fmt.Sprintf("case %d: output must be non-empty and specific to the task", i+1)
		}

		es.Cases = append(es.Cases, store.EvalCase{
			ID:       fmt.Sprintf("gen-%d", i+1),
			Input:    input,
			Criteria: criteria,
			Checks:   checks,
		})
	}

	return es
}

// generateEvalsetWithEngine is the internal implementation that accepts an
// injected engine — used by tests to inject mocks.
func generateEvalsetWithEngine(ctx context.Context, st *store.Store, skillID string, eng genEvalsetEngine) (store.Evalset, error) {
	// 1. Load the skill.
	sk, err := st.GetSkill(ctx, skillID)
	if err != nil {
		return store.Evalset{}, fmt.Errorf("get skill %q: %w", skillID, err)
	}

	// 2. Build and send the prompt.
	prompt := genEvalsetPrompt(skillID, sk.Instruction)
	text, rawJSON, err := eng.runRaw(ctx, prompt)
	if err != nil {
		return store.Evalset{}, fmt.Errorf("engine call for skill %q: %w", skillID, err)
	}

	// 3. Parse the model output.
	rawCases, err := parseGenCases(rawJSON, text)
	if err != nil {
		return store.Evalset{}, fmt.Errorf("parse evalset cases for skill %q: %w", skillID, err)
	}

	// 4. Build the Evalset.
	es := buildEvalset(skillID, rawCases)

	// 5. Persist.
	if err := st.UpsertEvalset(ctx, es); err != nil {
		return store.Evalset{}, fmt.Errorf("upsert evalset for skill %q: %w", skillID, err)
	}

	return es, nil
}

// GenerateEvalset generates a discriminating evalset for the given skill and
// persists it via store.UpsertEvalset.
//
// engineName should be "codex" or "local" (default: "local").
// Returns the stored Evalset on success.
func GenerateEvalset(ctx context.Context, st *store.Store, skillID, engineName string) (store.Evalset, error) {
	var eng genEvalsetEngine
	if engineName == "codex" {
		eng = &codexGenEngine{}
	} else {
		eng = &localGenEngine{}
	}
	return generateEvalsetWithEngine(ctx, st, skillID, eng)
}
