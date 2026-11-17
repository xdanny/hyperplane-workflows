package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xdanny/hyperplane-workflows/internal/config"
)

func readTerminalUntil(t *testing.T, ctx context.Context, c *websocket.Conn, want string) string {
	t.Helper()
	var output strings.Builder
	for !strings.Contains(output.String(), want) {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v, output %s", want, err, output.String())
		}
		output.Write(data)
	}
	return output.String()
}

func TestTerminalUsesSDKConsole(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_PLATFORM_ROOT", root)
	t.Setenv("HYPERPLANE_ORCHESTRATOR_CMD", "")
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal", "codex", "testdata", "console"))
	s := New(config.Load(), nil)
	server := httptest.NewServer(http.HandlerFunc(s.handleTerminalWS))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer s.Shutdown(ctx)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?kind=orchestrator", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	readTerminalUntil(t, ctx, c, "you>")
	if err := c.Write(ctx, websocket.MessageBinary, []byte("first\r")); err != nil {
		t.Fatal(err)
	}
	output := readTerminalUntil(t, ctx, c, "turn finished")
	if !strings.Contains(output, "reply: first") {
		t.Fatal(output)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("second\r")); err != nil {
		t.Fatal(err)
	}
	output = readTerminalUntil(t, ctx, c, "turn finished")
	if !strings.Contains(output, "reply: second") {
		t.Fatal(output)
	}
}

func TestTerminalDisconnectAndShutdownStopDescendants(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "disconnect"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "console")
			marker := filepath.Join(root, "escaped")
			t.Setenv("PTY_ESCAPE", marker)
			if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 1; echo escaped > \"$PTY_ESCAPE\") &\nprintf 'ready\\n'\nwait\n"), 0700); err != nil {
				t.Fatal(err)
			}
			s := New(config.Config{PlatformRoot: root, OrchestratorCmd: script}, nil)
			server := httptest.NewServer(http.HandlerFunc(s.handleTerminalWS))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			defer s.Shutdown(ctx)
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?kind=orchestrator", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			readTerminalUntil(t, ctx, c, "ready")
			if shutdown {
				if err := s.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				c.CloseNow()
			}
			time.Sleep(1200 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("terminal descendant survived %s: %v", name, err)
			}
			s.runner.mu.Lock()
			active := len(s.runner.active)
			s.runner.mu.Unlock()
			if active != 0 {
				t.Fatalf("%d sessions still owned", active)
			}
		})
	}
}

func TestTerminalRejectsUnknownKind(t *testing.T) {
	s := New(config.Config{}, nil)
	w := httptest.NewRecorder()
	s.handleTerminalWS(w, httptest.NewRequest("GET", "/api/terminal/ws?kind=typo", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown terminal kind: %d", w.Code)
	}
}
