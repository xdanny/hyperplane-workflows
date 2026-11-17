package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

func TestWorkflowValidationUsesSDKWithoutStorageOrSourceReads(t *testing.T) {
	// No database is supplied. A nonexistent dataset is valid declaration text:
	// source inspection belongs to execution, not editing or saving.
	root := t.TempDir()
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "must-not-run"))
	h := New(config.Config{RunsDir: root}, nil).Handler()
	node := map[string]any{"id": "profile", "kind": "task", "title": "Profile", "instruction": "Check these sources", "acceptance": "data-quality-profile", "inputs": map[string]string{"dataset": filepath.Join(root, "missing.csv"), "keyColumns": "id"}}
	call := func(body any) map[string]any {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/workflows/validate", strings.NewReader(string(data)))
		r.RemoteAddr = "127.0.0.1:1000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("validation: %d %s", w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["runtimeFingerprint"] != agentchecks.RuntimeFingerprint() || result["runtimeFingerprintVersion"] != float64(agentchecks.RuntimeFingerprintVersion) {
			t.Fatalf("unidentified validation: %v", result)
		}
		return result
	}
	body := map[string]any{"instruction": "Understand the data", "nodes": []any{node}}
	if result := call(body); result["valid"] != true {
		t.Fatalf("source was read or valid declaration refused: %v", result)
	}
	node["acceptance"] = "dataset-shift-profile"
	node["inputs"] = map[string]string{"baselineData": "/missing/baseline.csv", "currentData": "/missing/current.csv", "keyColumns": "id", "features": `[{"feature":"amount","type":"numeric","statistic":"psi","unit":"psi","tolerance":"0.1","watchBand":"0.2","whyItMatters":"changes totals"}]`}
	result := call(body)
	issue, ok := result["issue"].(map[string]any)
	if result["valid"] != false || !ok || issue["nodeId"] != "profile" || !strings.Contains(strings.ToLower(issue["message"].(string)), "watch") {
		t.Fatalf("SDK declaration issue missing: %v", result)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("validation created work: %v", entries)
	}
}

func TestWorkflowValidationRejectsMalformedBodiesAndUnavailableRuntime(t *testing.T) {
	h := New(config.Config{}, nil).Handler()
	for _, body := range []string{`{`, `{"instruction":"Goal","nodes":[],"launch":true}`, `{"instruction":"Goal","nodes":[]} {}`, `{"instruction":"Goal","nodes":[{"id":"one","title":"One","instruction":"Write","execute":true}]}`, strings.Repeat(" ", 1_048_577)} {
		r := httptest.NewRequest(http.MethodPost, "/api/workflows/validate", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("malformed validation: %d %s", w.Code, w.Body.String())
		}
	}
	t.Setenv("HYPERPLANE_PLATFORM_ROOT", t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/api/workflows/validate", strings.NewReader(`{"instruction":"Goal","nodes":[{"id":"one","title":"One","instruction":"Write"}]}`))
	r.RemoteAddr = "127.0.0.1:1000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("runtime unavailable must not look like bad user input: %d %s", w.Code, w.Body.String())
	}
	for route, body := range map[string]string{
		"/api/workflows": `{"name":"Draft","kind":"nodes","instruction":"Goal","nodes":[{"id":"one","title":"One","instruction":"Write"}]}`,
		"/api/run":       `{"goal":"Goal","nodes":[{"id":"one","title":"One","instruction":"Write"}]}`,
	} {
		r := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s reported unavailable validation as user error: %d %s", route, w.Code, w.Body.String())
		}
	}
}

func TestSDKDeclarationFailureCannotSaveOrLaunch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "must-not-run"))
	s := New(config.Config{RunsDir: root}, nil)
	node := map[string]any{"id": "baseline", "kind": "task", "title": "Baseline", "instruction": "Measure the change", "acceptance": "pipeline-change-profile", "inputs": map[string]string{
		"pipelineLocation": "/missing/pipeline", "changeRequest": "Update the label", "grain": "One row per id", "safeUse": "Synthetic inputs only", "keyColumns": "id", "runCommand": "run-pipeline", "invariants": `[{"invariant":"label","measure":"Label stays stable","kind":"text","unit":"absolute","tolerance":"1"}]`,
	}}
	// A semantic declaration failure must be refused even when the editor is bypassed.
	for _, route := range []string{"/api/workflows", "/api/run"} {
		body := map[string]any{"nodes": []any{node}, "goal": "Compare versions"}
		if route == "/api/workflows" {
			body = map[string]any{"name": "Compare", "kind": "nodes", "instruction": "Compare versions", "nodes": []any{node}}
		}
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, route, strings.NewReader(string(data)))
		r.RemoteAddr = "127.0.0.1:1000"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(w.Body.String()), "exact") {
			t.Fatalf("%s accepted invalid declaration: %d %s", route, w.Code, w.Body.String())
		}
	}
	if !s.DrainRuns(5 * time.Second) {
		t.Fatal("invalid launch left work active")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid declaration created work: %v", entries)
	}
}
