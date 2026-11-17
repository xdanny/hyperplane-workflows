package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func consoleManagementStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL") // TestMain supplies a disposable database only.
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	if err := dbtest.GuardTestDSN(dsn); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

func consoleRequest(t *testing.T, b *consoleManagement, method, path, token string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, b.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func TestConsoleManagementPersistsProjectsAndValidatedWorkflows(t *testing.T) {
	st := consoleManagementStore(t)
	s := New(config.Config{}, st)
	bridge, err := s.startConsoleManagement(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	projectID := "console-management-" + time.Now().Format("150405.000000000")
	status, data := consoleRequest(t, bridge, "POST", "/api/projects", bridge.Token, []byte(`{"id":"`+projectID+`","name":"Console project","summary":"Reviewed work"}`))
	if status != 201 {
		t.Fatalf("create project: %d %s", status, data)
	}
	status, data = consoleRequest(t, bridge, "PATCH", "/api/projects/"+projectID, bridge.Token, []byte(`{"summary":"Updated purpose"}`))
	if status != 200 {
		t.Fatalf("update project: %d %s", status, data)
	}
	project, err := st.GetProject(t.Context(), projectID)
	if err != nil || project.Summary != "Updated purpose" {
		t.Fatalf("project was not persisted: %+v %v", project, err)
	}
	for _, route := range []string{"/api/projects", "/api/projects/" + projectID, "/api/workflow-checks"} {
		status, _ := consoleRequest(t, bridge, "GET", route, bridge.Token, nil)
		if status != 200 {
			t.Fatalf("read %s: %d", route, status)
		}
	}
	invalid := []byte(`{"instruction":"Write notes","nodes":[{"id":"approve","kind":"approval","title":"Approve","instruction":"Approve"}]}`)
	status, data = consoleRequest(t, bridge, "POST", "/api/workflows/validate", bridge.Token, invalid)
	var validation struct{ Valid bool }
	if err := json.Unmarshal(data, &validation); err != nil || status != 200 || validation.Valid {
		t.Fatalf("invalid declaration: %d %s %v", status, data, err)
	}
	status, data = consoleRequest(t, bridge, "POST", "/api/workflows", bridge.Token, []byte(`{"name":"Invalid","kind":"nodes","instruction":"Write notes","nodes":[{"id":"approve","kind":"approval","title":"Approve","instruction":"Approve"}]}`))
	if status != 400 {
		t.Fatalf("invalid workflow saved: %d %s", status, data)
	}
	status, data = consoleRequest(t, bridge, "POST", "/api/workflows", bridge.Token, []byte(`{"name":"Notes","kind":"nodes","instruction":"Write notes","project":"`+projectID+`","nodes":[{"id":"write","title":"Write","instruction":"Describe the result"}]}`))
	var workflow store.SDKWorkflow
	if err := json.Unmarshal(data, &workflow); err != nil || status != 201 || workflow.ID == "" || workflow.Revision != 1 {
		t.Fatalf("save workflow: %d %s %v", status, data, err)
	}
	workflow.Name = "Revised notes"
	payload, err := json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	status, data = consoleRequest(t, bridge, "PUT", "/api/workflows/"+workflow.ID, bridge.Token, payload)
	if status != 200 {
		t.Fatalf("update workflow: %d %s", status, data)
	}
	status, _ = consoleRequest(t, bridge, "PUT", "/api/workflows/"+workflow.ID, bridge.Token, payload)
	if status != 409 {
		t.Fatalf("stale workflow revision accepted: %d", status)
	}
	workflows, err := st.ListSDKWorkflows(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, saved := range workflows {
		if saved.ID == workflow.ID {
			found = saved.Name == "Revised notes" && saved.Revision == 2 && saved.Nodes[0].Instruction == "Describe the result"
		}
	}
	if !found {
		t.Fatal("saved workflow missing actual edited settings")
	}
	var count int
	if err := st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM runs WHERE project=$1`, projectID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("management started work: %d %v", count, err)
	}
}

func TestConsoleManagementRejectsBroaderCapabilitiesAndOversizedBodies(t *testing.T) {
	s := New(config.Config{}, nil)
	bridge, err := s.startConsoleManagement(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	for _, token := range []string{"", "wrong", bridge.Token + "extra"} {
		status, _ := consoleRequest(t, bridge, "GET", "/api/workflow-checks", token, nil)
		if status != 401 {
			t.Fatalf("invalid token admitted: %d", status)
		}
	}
	for _, route := range []struct{ method, path string }{{"POST", "/api/run"}, {"GET", "/api/runs"}, {"GET", "/api/terminal/ws?kind=shell"}, {"POST", "/api/auth/login"}, {"DELETE", "/api/workflows/example"}, {"DELETE", "/api/projects/example"}, {"GET", "/file/example"}, {"HEAD", "/api/projects"}} {
		status, _ := consoleRequest(t, bridge, route.method, route.path, bridge.Token, []byte(`{}`))
		if status != 404 && status != 405 {
			t.Fatalf("broader capability admitted: %s %s: %d", route.method, route.path, status)
		}
	}
	status, _ := consoleRequest(t, bridge, "POST", "/api/projects", bridge.Token, append([]byte(`{}`), bytes.Repeat([]byte(" "), consoleManagementBodyLimit)...))
	if status != 413 {
		t.Fatalf("oversized trailing body admitted: %d", status)
	}
}

func TestConsoleManagementCloseCancelsActiveDatabaseRead(t *testing.T) {
	st := consoleManagementStore(t)
	s := New(config.Config{}, st)
	bridge, err := s.startConsoleManagement(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	tx, err := st.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `LOCK TABLE sdk_workflows IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), "GET", bridge.URL+"/api/workflows", nil)
	req.Header.Set("Authorization", "Bearer "+bridge.Token)
	done := make(chan error, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int
		if err := st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT %sdk_workflows%'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("management read never reached blocked query")
		}
		time.Sleep(10 * time.Millisecond)
	}
	bridge.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("management read survived session close")
	}
	if resp, err := (&http.Client{Timeout: time.Second}).Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("closed listener still accepted session token")
	}
}

func TestConsoleTerminalCapabilityEndsWithSession(t *testing.T) {
	for _, mode := range []string{"default-disconnect", "default-shutdown", "custom", "shell"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			command := filepath.Join(root, "scripts", "orchestrator.sh")
			if mode == "custom" || mode == "shell" {
				command = filepath.Join(root, "custom")
			}
			if err := os.MkdirAll(filepath.Dir(command), 0700); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(root, "capability")
			t.Setenv("CONSOLE_TEST_CAPABILITY_FILE", record)
			t.Setenv("HYPERPLANE_CONSOLE_API", "must-not-inherit")
			t.Setenv("HYPERPLANE_CONSOLE_TOKEN", "must-not-inherit")
			script := "#!/bin/sh\numask 077\nprintf '%s\\n%s\\n' \"$HYPERPLANE_CONSOLE_API\" \"$HYPERPLANE_CONSOLE_TOKEN\" > \"$CONSOLE_TEST_CAPABILITY_FILE\"\nprintf 'ready\\n'\nsleep 300\n"
			if err := os.WriteFile(command, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			s := New(config.Config{PlatformRoot: root, OrchestratorCmd: command, ShellCmd: command}, nil)
			server := httptest.NewServer(http.HandlerFunc(s.handleTerminalWS))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			defer s.Shutdown(context.Background())
			kind := "orchestrator"
			if mode == "shell" {
				kind = "shell"
			}
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?kind="+kind, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			readTerminalUntil(t, ctx, c, "ready")
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			if len(fields) != 2 {
				t.Fatal("invalid capability record")
			}
			if mode == "custom" || mode == "shell" {
				if fields[0] != "" || fields[1] != "" {
					t.Fatal("unrelated command inherited management capability")
				}
				c.CloseNow()
				return
			}
			bridge := &consoleManagement{URL: fields[0], Token: fields[1]}
			status, _ := consoleRequest(t, bridge, "GET", "/api/workflow-checks", bridge.Token, nil)
			if status != 200 {
				t.Fatalf("default console missing capability: %d", status)
			}
			if mode == "default-shutdown" {
				if err := s.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				c.CloseNow()
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				resp, err := (&http.Client{Timeout: time.Second}).Get(bridge.URL + "/api/workflow-checks")
				if err != nil {
					break
				}
				resp.Body.Close()
				if time.Now().After(deadline) {
					t.Fatal("session listener survived terminal end")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
