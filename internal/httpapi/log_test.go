package httpapi

// Tests for GET /api/runs/{id}/log — the endpoint the run inspector's log panel
// reads. These deliberately build a Server with only a config: the handler
// touches the filesystem and never the store, so the log contract stays covered
// even when no test database is reachable.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
)

// newLogTestServer returns a mux wired to the two log routes plus the runs dir
// it serves, with one run directory already created.
func newLogTestServer(t *testing.T, runID string) (http.Handler, string) {
	t.Helper()
	runsDir := t.TempDir()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	s := &Server{cfg: config.Config{RunsDir: runsDir}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs/{id}/log", s.handleGetLog)
	mux.HandleFunc("GET /api/runs/{id}/log/{nodeId}", s.handleGetLog)
	return mux, runDir
}

func getLog(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// TestGetLogServesNodeLog covers the happy path through both route shapes: the
// path-segment form the web app uses and the query-param form the CLI uses.
func TestGetLogServesNodeLog(t *testing.T) {
	h, runDir := newLogTestServer(t, "run-1")
	const body = "line one\nline two\n"
	if err := os.WriteFile(filepath.Join(runDir, "build.log"), []byte(body), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	for _, target := range []string{
		"/api/runs/run-1/log/build",
		"/api/runs/run-1/log?nodeId=build",
	} {
		rec := getLog(t, h, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d, want 200", target, rec.Code)
		}
		if rec.Body.String() != body {
			t.Errorf("%s: got %q, want %q", target, rec.Body.String(), body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: Content-Type = %q, want text/plain", target, ct)
		}
		if got := rec.Header().Get("X-Log-Size"); got != strconv.Itoa(len(body)) {
			t.Errorf("%s: X-Log-Size = %q, want %d", target, got, len(body))
		}
		if rec.Header().Get("X-Log-Truncated") != "" {
			t.Errorf("%s: whole log served, X-Log-Truncated must be unset", target)
		}
	}
}

// TestGetLogTail pins ?tail=N semantics: the last N lines, the whole log when N
// exceeds the line count, and truncation signalled only when lines were dropped.
func TestGetLogTail(t *testing.T) {
	h, runDir := newLogTestServer(t, "run-1")
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	full := sb.String()
	if err := os.WriteFile(filepath.Join(runDir, "n1.log"), []byte(full), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	tests := []struct {
		name      string
		tail      string
		want      string
		truncated bool
	}{
		{"last three lines", "3", "line 8\nline 9\nline 10\n", true},
		{"single last line", "1", "line 10\n", true},
		{"exactly the line count", "10", full, false},
		{"more than the line count", "99", full, false},
		{"zero falls back to whole log", "0", full, false},
		{"negative falls back to whole log", "-5", full, false},
		{"malformed falls back to whole log", "abc", full, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getLog(t, h, "/api/runs/run-1/log/n1?tail="+tc.tail)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200", rec.Code)
			}
			if rec.Body.String() != tc.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.want)
			}
			gotTrunc := rec.Header().Get("X-Log-Truncated") == "true"
			if gotTrunc != tc.truncated {
				t.Errorf("X-Log-Truncated = %v, want %v", gotTrunc, tc.truncated)
			}
			// X-Log-Size always reports the whole file, never the slice served.
			if got := rec.Header().Get("X-Log-Size"); got != strconv.Itoa(len(full)) {
				t.Errorf("X-Log-Size = %q, want %d", got, len(full))
			}
		})
	}
}

// TestGetLogCapsOversizeRead asserts the byte cap still bounds the response for
// a log larger than maxLogBytes, and that a tail applies within that window.
func TestGetLogCapsOversizeRead(t *testing.T) {
	h, runDir := newLogTestServer(t, "run-1")
	line := strings.Repeat("x", 99) + "\n" // 100 bytes per line
	big := strings.Repeat(line, (maxLogBytes/100)+500)
	if err := os.WriteFile(filepath.Join(runDir, "big.log"), []byte(big), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	rec := getLog(t, h, "/api/runs/run-1/log/big")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if rec.Body.Len() > maxLogBytes {
		t.Errorf("body is %d bytes, must not exceed the %d cap", rec.Body.Len(), maxLogBytes)
	}
	if rec.Header().Get("X-Log-Truncated") != "true" {
		t.Error("oversize log must report X-Log-Truncated")
	}
	if got := rec.Header().Get("X-Log-Size"); got != strconv.Itoa(len(big)) {
		t.Errorf("X-Log-Size = %q, want the full %d", got, len(big))
	}

	rec2 := getLog(t, h, "/api/runs/run-1/log/big?tail=5")
	if want := strings.Repeat(line, 5); rec2.Body.String() != want {
		t.Errorf("tail=5 over an oversize log returned %d bytes, want %d", rec2.Body.Len(), len(want))
	}
}

// TestGetLogRejectsTraversal is the path-safety guard: no nodeId or run id may
// address a file outside its own run directory, and a miss inside the run dir
// is a 404 rather than a leak.
func TestGetLogRejectsTraversal(t *testing.T) {
	h, runDir := newLogTestServer(t, "run-1")

	// A secret one level above the run dir — the target of every attempt below.
	secret := filepath.Join(filepath.Dir(runDir), "secret.log")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	tests := []struct {
		name   string
		target string
	}{
		{"dotdot in nodeId query", "/api/runs/run-1/log?nodeId=../secret"},
		{"dotdot chain in nodeId query", "/api/runs/run-1/log?nodeId=../../etc/passwd"},
		{"slash in nodeId query", "/api/runs/run-1/log?nodeId=sub/other"},
		{"encoded dotdot in nodeId path", "/api/runs/run-1/log/..%2Fsecret"},
		{"dotdot in run id", "/api/runs/..%2Frun-1/log?nodeId=n1"},
		{"absolute-ish nodeId", `/api/runs/run-1/log?nodeId=%5Cetc%5Cpasswd`},
		{"missing nodeId", "/api/runs/run-1/log"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getLog(t, h, tc.target)
			if rec.Code == http.StatusOK {
				t.Fatalf("got 200 — traversal was not rejected (body %q)", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "TOP SECRET") {
				t.Fatal("response leaked a file from outside the run directory")
			}
		})
	}

	// A well-formed request for a log that simply does not exist is a 404.
	if rec := getLog(t, h, "/api/runs/run-1/log/absent"); rec.Code != http.StatusNotFound {
		t.Errorf("missing log: got %d, want 404", rec.Code)
	}
}

// TestLastLines exercises the tail slicer directly, including the edges the
// handler tests cannot reach cheaply (no trailing newline, empty input).
func TestLastLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"trailing newline kept", "a\nb\nc\n", 2, "b\nc\n"},
		{"no trailing newline", "a\nb\nc", 2, "b\nc"},
		{"single line no newline", "only", 1, "only"},
		{"n exceeds lines", "a\nb\n", 10, "a\nb\n"},
		{"empty input", "", 3, ""},
		{"n zero returns input", "a\nb\n", 0, "a\nb\n"},
		{"blank lines counted", "a\n\n\nb\n", 2, "\nb\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(lastLines([]byte(tc.in), tc.n)); got != tc.want {
				t.Errorf("lastLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}
