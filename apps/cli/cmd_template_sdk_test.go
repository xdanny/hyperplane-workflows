package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTemplateCLIPresentsSDKDefinitionAndLaunch(t *testing.T) {
	for _, run := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" && r.URL.Path == "/api/templates" {
					_, _ = w.Write([]byte(`[{"id":"sdk-template","name":"deep-research"}]`))
					return
				}
				if r.Method != "POST" || r.URL.Path != "/api/templates/sdk-template/instantiate" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					Run   bool
					Slots map[string]string
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Run != run || body.Slots["question"] != "Compare choices" {
					t.Errorf("request changed: %+v", body)
				}
				out := map[string]any{"workflow": map[string]any{"kind": "nodes", "name": "deep-research", "instruction": "Compare choices", "nodes": []map[string]any{{"id": "sources", "title": "Find evidence", "kind": "research", "instruction": "Cite sources"}, {"id": "review", "title": "Review evidence", "kind": "check", "instruction": "Reject unsupported claims"}, {"id": "profile", "title": "Measure data", "kind": "task", "instruction": "Measure source", "acceptance": "data-quality-profile", "inputs": map[string]string{"dataset": "/data/source.csv", "keyColumns": "id"}}}}}
				if run {
					out["runId"] = "sdk-run"
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(out)
			}))
			t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
			args := []string{"deep-research", "--slot", "question=Compare choices"}
			if run {
				args = append(args, "--run")
			}
			if asJSON {
				args = append(args, "--json")
			}
			file, err := os.CreateTemp(t.TempDir(), "stdout")
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stdout
			var exit int
			func() {
				os.Stdout = file
				defer func() { os.Stdout = original }()
				exit = cmdTemplateInstantiate(args, true)
			}()
			_ = file.Close()
			server.Close()
			data, err := os.ReadFile(file.Name())
			if err != nil || exit != 0 {
				t.Fatalf("CLI failed: %d %v", exit, err)
			}
			output := string(data)
			if asJSON {
				var response map[string]json.RawMessage
				if json.Unmarshal(data, &response) != nil || len(response["workflow"]) == 0 || len(response["draft"]) > 0 {
					t.Fatalf("lost editable JSON: %s", data)
				}
			} else if !strings.Contains(output, "Built-in acceptance: data-quality-profile") || !strings.Contains(output, "Source: /data/source.csv") || !strings.Contains(output, "Key columns: id") || !strings.Contains(output, "Find evidence (research)") || !strings.Contains(output, "Reject unsupported claims") || (!run && !strings.Contains(output, "not saved or started")) || (run && !strings.Contains(output, "hyperplane watch sdk-run")) {
				t.Fatalf("incomplete preparation/launch output: %s", output)
			}
		}
	}
}

func TestTemplateCLIRejectsRetiredAndUnknownFlagsBeforeRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid flags reached API"); w.WriteHeader(500) }))
	defer server.Close()
	t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
	for _, args := range [][]string{{"ml-engineering", "--dry-run"}, {"ml-engineering", "--goal", "replacement"}, {"ml-engineering", "--unknown"}} {
		if got := cmdTemplateInstantiate(args, true); got != 2 {
			t.Fatalf("invalid flags returned %d: %v", got, args)
		}
	}
}

func TestTemplateCLIRejectsGraphResponsesInBothOutputModes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"draft":{"id":"old-graph"}}`))
	}))
	defer server.Close()
	t.Setenv("HYPERPLANE_ADDR", strings.TrimPrefix(server.URL, "http://"))
	for _, args := range [][]string{{"ml-engineering"}, {"ml-engineering", "--json"}} {
		if got := cmdTemplateInstantiate(args, true); got != 1 {
			t.Fatalf("graph response returned %d: %v", got, args)
		}
	}
}
