// Package localmodel uses the official OpenAI SDK for local text generation
// (e.g. LM Studio at http://localhost:1234/v1). It mirrors the codex adapter
// contract so existing callers use their explicitly configured model provider.
//
// Configuration (all optional, env-driven):
//
//	LOCAL_LLM_BASE_URL  base URL of the OpenAI-compatible server (default: http://localhost:1234/v1)
//	LOCAL_LLM_MODEL     model name to use (default: qwen3.6-35b-a3b-ud-mlx)
//	LOCAL_LLM_API_KEY   bearer token, if the server requires one
//
// Workflow sessions and tools use the separate Codex SDK runtime.
package localmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/xdanny/hyperplane-workflows/internal/contract"
)

const (
	defaultBaseURL = "http://localhost:1234/v1"
	defaultModel   = "qwen3.6-35b-a3b-ud-mlx"
	// DefaultTimeout bounds a single local model call.
	DefaultTimeout = 120 * time.Second
)

// Options configures a single localmodel call. It mirrors codex.Options for the
// fields that are relevant to an HTTP chat-completion API.
type Options struct {
	// Cwd resolves relative context and schema paths for the owning caller.
	Cwd string
	// Instruction is the user-facing prompt / task description.
	Instruction string
	// ContextFiles lists file paths whose contents are inlined into the user
	// message (relative to Cwd when provided, otherwise the process cwd).
	ContextFiles []string
	// OutputSchemaPath, when non-empty, switches the call to JSON mode:
	// the response_format is set to json_object and the model is told to
	// reply with only JSON. The application validates the actual schema afterward.
	OutputSchemaPath string
	// Model overrides LOCAL_LLM_MODEL for this call.
	Model string
	// Reasoning is accepted but currently ignored for local models.
	Reasoning string
	// SystemPrompt is an optional system message prepended to the conversation.
	SystemPrompt string
}

// Result is the parsed outcome of a localmodel call. It mirrors codex.Result
// for the fields that are available from an OpenAI-compatible response.
type Result struct {
	OK           bool            `json:"ok"`
	Text         string          `json:"text"`
	JSON         json.RawMessage `json:"json,omitempty"`
	ErrorMsg     string          `json:"error,omitempty"`
	InputTokens  int             `json:"inputTokens,omitempty"`
	OutputTokens int             `json:"outputTokens,omitempty"`
}

// sdkOptions deliberately configures SDK services directly. Their constructors
// do not inherit OPENAI_* credentials, custom headers or account configuration.
// Only LOCAL_LLM_* settings belong to this separately configured provider.
func sdkOptions() []option.RequestOption {
	base := os.Getenv("LOCAL_LLM_BASE_URL")
	if base == "" {
		base = defaultBaseURL
	}
	return []option.RequestOption{option.WithBaseURL(base), option.WithAPIKey(os.Getenv("LOCAL_LLM_API_KEY")), option.WithMaxRetries(0)}
}

func modelName(override string) string {
	if override != "" {
		return override
	}
	if model := os.Getenv("LOCAL_LLM_MODEL"); model != "" {
		return model
	}
	return defaultModel
}

// Run uses the official OpenAI SDK for text-only generation. It grants no
// agent tools. Retry and workflow recovery decisions stay with the caller.
func Run(ctx context.Context, opts Options) (Result, error) {
	fail := func(err error) (Result, error) { return Result{ErrorMsg: err.Error()}, err }
	userContent := opts.Instruction
	for _, name := range opts.ContextFiles {
		path := name
		if !filepath.IsAbs(path) && opts.Cwd != "" {
			path = filepath.Join(opts.Cwd, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fail(fmt.Errorf("read context %s: %w", name, err))
		}
		userContent += fmt.Sprintf("\n\nCONTEXT FILE — %s:\n%s", name, data)
	}
	var schema any
	if opts.OutputSchemaPath != "" {
		path := opts.OutputSchemaPath
		if !filepath.IsAbs(path) && opts.Cwd != "" {
			path = filepath.Join(opts.Cwd, path)
		}
		var err error
		schema, err = contract.ReadSchema(filepath.Base(path), filepath.Dir(path))
		if err != nil {
			return fail(err)
		}
		schemaJSON, _ := json.Marshal(schema)
		userContent += "\n\nReply with only JSON. It must satisfy this unchanged schema:\n" + string(schemaJSON)
	}
	params := openai.ChatCompletionNewParams{Model: modelName(opts.Model)}
	if opts.SystemPrompt != "" {
		params.Messages = append(params.Messages, openai.SystemMessage(opts.SystemPrompt))
	}
	params.Messages = append(params.Messages, openai.UserMessage(userContent))
	if opts.OutputSchemaPath != "" {
		params.ResponseFormat.OfJSONObject = &shared.ResponseFormatJSONObjectParam{Type: "json_object"}
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	service := openai.NewChatCompletionService(sdkOptions()...)
	response, err := service.New(ctx, params)
	if err != nil {
		return fail(err)
	}
	if len(response.Choices) == 0 {
		return fail(fmt.Errorf("local model returned no choices"))
	}
	choice := response.Choices[0]
	text := strings.TrimSpace(choice.Message.Content)
	result := Result{Text: text, JSON: extractJSON(text), InputTokens: int(response.Usage.PromptTokens), OutputTokens: int(response.Usage.CompletionTokens)}
	if text == "" || choice.Message.Refusal != "" || (choice.FinishReason != "" && choice.FinishReason != "stop") {
		err = fmt.Errorf("local model did not finish a usable text response (finish reason %q)", choice.FinishReason)
	} else if opts.OutputSchemaPath != "" {
		var value any
		if json.Unmarshal([]byte(text), &value) != nil {
			err = fmt.Errorf("local model returned invalid JSON")
		} else if finding := contract.ValidateSchema(value, schema, "local model output"); finding != "" {
			err = fmt.Errorf("%s", finding)
		}
	}
	if err != nil {
		result.ErrorMsg = err.Error()
		return result, err
	}
	result.OK = true
	return result, nil
}

// Reachable checks the configured model list through the same explicit SDK
// provider settings. A successful HTTP response with no model list is not ready.
func Reachable(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	service := openai.NewModelService(sdkOptions()...)
	models, err := service.List(ctx)
	if err != nil {
		return false, err.Error()
	}
	if len(models.Data) == 0 {
		return false, "model list is empty"
	}
	configured := modelName("")
	for _, model := range models.Data {
		if model.ID == configured {
			return true, configured
		}
	}
	return false, fmt.Sprintf("configured model %q is not advertised by the server", configured)
}

// extractJSON returns the first parseable JSON object or array in text, or nil.
// Mirrors the same helper in the codex package so JSON-mode outputs work
// identically regardless of which engine produced them.
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
