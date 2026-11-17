package eval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
)

// buildEvalPrompt combines the target instruction with the case input.
func buildEvalPrompt(instruction string, input json.RawMessage) string {
	p := instruction
	if len(input) > 0 {
		p += "\n\nINPUT:\n" + string(input)
	}
	return p
}

// asJSONOut returns the structured JSON when present, else the text wrapped as a
// JSON string — so the Engine contract always yields valid json.RawMessage.
func asJSONOut(j json.RawMessage, text string) json.RawMessage {
	if len(j) > 0 {
		return j
	}
	b, _ := json.Marshal(text)
	return b
}

// CodexEngine runs eval cases through the codex adapter (the billed engine).
// codex/localmodel are leaf adapters, so importing them here creates no cycle.
type CodexEngine struct {
	Model  string
	RunDir string
	CWD    string
}

func (e *CodexEngine) Run(ctx context.Context, instruction string, input json.RawMessage) (json.RawMessage, error) {
	res, err := codex.Run(ctx, codex.Options{
		Instruction: buildEvalPrompt(instruction, input),
		Model:       e.Model,
		Reasoning:   "low",
		Cwd:         e.CWD,
		RunDir:      e.RunDir,
		Label:       "eval",
		SandboxMode: codex.SandboxReadOnly,
	})
	if err != nil {
		return nil, err
	}
	if res.RateLimited {
		return nil, fmt.Errorf("codex rate-limited")
	}
	if !res.OK {
		return nil, fmt.Errorf("codex failed (status %d)", res.Status)
	}
	return asJSONOut(res.JSON, res.Text), nil
}

// LocalEngine runs eval cases through a local OpenAI-compatible model (LM Studio /
// Unsloth-served). Free + offline — the point of evals is to find which tasks it
// handles as well as codex so they can be routed here.
type LocalEngine struct {
	Model string // empty ⇒ LOCAL_LLM_MODEL / default
}

func (e *LocalEngine) Run(ctx context.Context, instruction string, input json.RawMessage) (json.RawMessage, error) {
	res, err := localmodel.Run(ctx, localmodel.Options{
		Instruction: buildEvalPrompt(instruction, input),
		Model:       e.Model,
	})
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("local model returned no usable output")
	}
	return asJSONOut(res.JSON, res.Text), nil
}

// MockEngine is a deterministic fake for tests.
type MockEngine struct {
	// Responses maps caseID -> response JSON; use "" key as default.
	Responses map[string]json.RawMessage
}

func (e *MockEngine) Run(ctx context.Context, instruction string, input json.RawMessage) (json.RawMessage, error) {
	if r, ok := e.Responses[instruction]; ok {
		return r, nil
	}
	if r, ok := e.Responses[""]; ok {
		return r, nil
	}
	return json.RawMessage(`"mock output"`), nil
}
