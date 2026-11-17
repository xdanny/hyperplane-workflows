package localmodel_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
)

// cannedResponse builds a minimal OpenAI-compatible chat-completion response.
func cannedResponse(content string, promptTokens, completionTokens int) []byte {
	resp := map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"model":   "test-model",
		"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}, "index": 0, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestRun_BasicText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse("Four.", 10, 3))
	}))
	defer srv.Close()

	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/v1")
	t.Setenv("LOCAL_LLM_MODEL", "test-model")
	os.Unsetenv("LOCAL_LLM_API_KEY")

	res, err := localmodel.Run(context.Background(), localmodel.Options{
		Instruction: "What is 2+2?",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true, got false: %s", res.ErrorMsg)
	}
	if res.Text != "Four." {
		t.Errorf("expected Text='Four.' got %q", res.Text)
	}
	if res.InputTokens != 10 {
		t.Errorf("expected InputTokens=10, got %d", res.InputTokens)
	}
	if res.OutputTokens != 3 {
		t.Errorf("expected OutputTokens=3, got %d", res.OutputTokens)
	}
	// No JSON output when reply is plain text.
	if res.JSON != nil {
		t.Errorf("expected JSON=nil, got %s", res.JSON)
	}
}

func TestRun_JSONExtraction(t *testing.T) {
	payload := `{"answer": 4, "reasoning": "2+2=4"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request body asks for JSON mode.
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if rf, ok := body["response_format"].(map[string]any); !ok || rf["type"] != "json_object" {
			t.Error("expected response_format.type=json_object in request")
		}
		// Verify the user message contains the JSON directive.
		msgs, _ := body["messages"].([]any)
		userMsg := ""
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "user" {
				userMsg, _ = mm["content"].(string)
			}
		}
		if !strings.Contains(userMsg, "Reply with only JSON") {
			t.Errorf("expected 'Reply with only JSON' directive in user message, got: %q", userMsg)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse(payload, 20, 15))
	}))
	defer srv.Close()

	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/v1")

	// Create a dummy schema file so OutputSchemaPath is non-empty.
	schemaFile, err := os.CreateTemp("", "schema-*.json")
	if err != nil {
		t.Fatal(err)
	}
	schemaFile.WriteString(`{"type":"object"}`)
	schemaFile.Close()
	defer os.Remove(schemaFile.Name())

	res, err := localmodel.Run(context.Background(), localmodel.Options{
		Instruction:      "What is 2+2?",
		OutputSchemaPath: schemaFile.Name(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true: %s", res.ErrorMsg)
	}
	if res.JSON == nil {
		t.Fatal("expected JSON to be extracted, got nil")
	}
	var got map[string]any
	if err := json.Unmarshal(res.JSON, &got); err != nil {
		t.Fatalf("JSON extraction produced invalid JSON: %v", err)
	}
	if got["answer"] != float64(4) {
		t.Errorf("expected answer=4, got %v", got["answer"])
	}
	if res.InputTokens != 20 {
		t.Errorf("expected InputTokens=20, got %d", res.InputTokens)
	}
	if res.OutputTokens != 15 {
		t.Errorf("expected OutputTokens=15, got %d", res.OutputTokens)
	}
}

func TestRun_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/v1")

	res, err := localmodel.Run(context.Background(), localmodel.Options{Instruction: "hello"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if res.OK {
		t.Error("expected OK=false on HTTP error")
	}
	if res.ErrorMsg == "" {
		t.Error("expected non-empty ErrorMsg")
	}
}

func TestRun_BearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse("ok", 5, 2))
	}))
	defer srv.Close()

	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/v1")
	t.Setenv("LOCAL_LLM_API_KEY", "secret-key")
	defer os.Unsetenv("LOCAL_LLM_API_KEY")

	res, err := localmodel.Run(context.Background(), localmodel.Options{Instruction: "ping"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true: %s", res.ErrorMsg)
	}
}

func TestRun_ContextFilesInlined(t *testing.T) {
	// Write a temp context file.
	cf, err := os.CreateTemp("", "ctx-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	cf.WriteString("the answer is 42")
	cf.Close()
	defer os.Remove(cf.Name())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		msgs, _ := body["messages"].([]any)
		userContent := ""
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "user" {
				userContent, _ = mm["content"].(string)
			}
		}
		if !strings.Contains(userContent, "the answer is 42") {
			t.Errorf("expected context file content in user message, got: %q", userContent)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse("42", 8, 2))
	}))
	defer srv.Close()

	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/v1")

	res, err := localmodel.Run(context.Background(), localmodel.Options{
		Instruction:  "What is the answer?",
		ContextFiles: []string{cf.Name()},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK || res.Text != "42" {
		t.Errorf("unexpected result: ok=%v text=%q err=%s", res.OK, res.Text, res.ErrorMsg)
	}
}

func TestReachable_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "qwen3.5-35b-a3b"}},
		})
	}))
	defer srv.Close()

	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/v1")
	t.Setenv("LOCAL_LLM_MODEL", "qwen3.5-35b-a3b")

	ok, info := localmodel.Reachable(context.Background())
	if !ok {
		t.Fatalf("expected reachable, got false: %s", info)
	}
	if info != "qwen3.5-35b-a3b" {
		t.Errorf("expected model name in info, got %q", info)
	}
}

func TestReachable_Unreachable(t *testing.T) {
	// Point at a port that is almost certainly closed.
	t.Setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:19999/v1")

	ok, info := localmodel.Reachable(context.Background())
	if ok {
		t.Fatal("expected unreachable, got reachable")
	}
	if info == "" {
		t.Error("expected non-empty error info when unreachable")
	}
}
