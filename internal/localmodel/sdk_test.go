package localmodel_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
)

func TestSDKProviderDoesNotInheritOpenAIAccountSettings(t *testing.T) {
	for _, localKey := range []string{"", "explicit-local-key"} {
		t.Run(localKey, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "unrelated-cloud-key")
			t.Setenv("OPENAI_ADMIN_KEY", "unrelated-admin-key")
			t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1/incorrect")
			t.Setenv("OPENAI_ORG_ID", "unrelated-organization")
			t.Setenv("OPENAI_PROJECT_ID", "unrelated-project")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer unrelated-header-key\nX-Private: unrelated-private-header")
			t.Setenv("LOCAL_LLM_API_KEY", localKey)
			t.Setenv("LOCAL_LLM_MODEL", "configured-model")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantAuth := ""
				if localKey != "" {
					wantAuth = "Bearer " + localKey
				}
				if r.Header.Get("Authorization") != wantAuth || r.Header.Get("OpenAI-Organization") != "" || r.Header.Get("OpenAI-Project") != "" || r.Header.Get("X-Private") != "" {
					t.Error("SDK inherited unrelated provider credentials or headers")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/provider/v1/models":
					w.Write([]byte(`{"data":[{"id":"configured-model"}]}`))
				case "/provider/v1/chat/completions":
					w.Write(cannedResponse("done", 10, 2))
				default:
					t.Errorf("incorrect provider path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("LOCAL_LLM_BASE_URL", srv.URL+"/provider/v1")
			if result, err := localmodel.Run(context.Background(), localmodel.Options{Instruction: "work"}); err != nil || !result.OK {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if ok, why := localmodel.Reachable(context.Background()); !ok {
				t.Fatal(why)
			}
		})
	}
}

func TestSDKFailuresDoNotRetryOrIgnoreCancellation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL)
	if _, err := localmodel.Run(context.Background(), localmodel.Options{Instruction: "work"}); err == nil {
		t.Fatal("HTTP failure passed")
	}
	if calls.Load() != 1 {
		t.Fatalf("hidden SDK retries: %d", calls.Load())
	}
	entered := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", slow.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := localmodel.Run(ctx, localmodel.Options{Instruction: "work"}); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation passed")
		}
	case <-time.After(time.Second):
		t.Fatal("SDK ignored cancellation")
	}
}

func TestSDKContextAndSchemaFailBeforeModelRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(cannedResponse("done", 1, 1)) }))
	defer srv.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL)
	dir := t.TempDir()
	for _, options := range []localmodel.Options{{Cwd: dir, ContextFiles: []string{"missing.txt"}}, {Cwd: dir, OutputSchemaPath: "missing.json"}} {
		if _, err := localmodel.Run(context.Background(), options); err == nil {
			t.Fatal("missing input was ignored")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "external.json"), []byte(`{"$ref":"https://example.invalid/schema"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := localmodel.Run(context.Background(), localmodel.Options{Cwd: dir, OutputSchemaPath: "external.json"}); err == nil {
		t.Fatal("external schema accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("model called with missing or invalid inputs: %d", calls.Load())
	}
}

func TestSDKResolvesOwnerContextAndChecksReturnedSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "context.txt"), []byte("node-owned context"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"integer"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		if len(input.Messages) != 1 || !strings.Contains(input.Messages[0].Content, "node-owned context") {
			t.Error("context resolved outside owning cwd")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse(`{"answer":"wrong type"}`, 12, 3))
	}))
	defer srv.Close()
	t.Setenv("LOCAL_LLM_BASE_URL", srv.URL)
	result, err := localmodel.Run(context.Background(), localmodel.Options{Cwd: dir, ContextFiles: []string{"context.txt"}, OutputSchemaPath: "schema.json"})
	if err == nil || result.OK || result.InputTokens != 12 || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("schema-invalid output passed or usage lost: %+v %v", result, err)
	}
}

func TestSDKReadinessRequiresAdvertisedModel(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"id":"another-model"}]}`, `{"unexpected":"shape"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}))
		t.Setenv("LOCAL_LLM_BASE_URL", srv.URL)
		t.Setenv("LOCAL_LLM_MODEL", "wanted-model")
		ok, why := localmodel.Reachable(context.Background())
		srv.Close()
		if ok || why == "" {
			t.Fatalf("unusable model list passed: %s", body)
		}
	}
}
