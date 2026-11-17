package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/httpapi"
)

// fileServer builds a Server backed only by a temp runs dir. GET /file/… never
// touches the store, so this needs no database and always runs in the gate.
// It returns the test server, the runs dir and the run id it seeded.
func fileServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()

	runsDir := t.TempDir()
	const runID = "20260101-120000.000-reader"
	artifacts := filepath.Join(runsDir, runID, "artifacts")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "report.md"), []byte("# Report\n\nbody text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Addr:         "127.0.0.1:0",
		PlatformRoot: t.TempDir(),
		TargetRoot:   t.TempDir(),
		RunsDir:      runsDir,
	}
	ts := httptest.NewServer(httpapi.New(cfg, nil).Handler())
	t.Cleanup(ts.Close)
	return ts, runsDir, runID
}

func TestHandleFileServesArtifact(t *testing.T) {
	ts, _, runID := fileServer(t)

	res, err := http.Get(ts.URL + "/file/" + runID + "/artifacts/report.md")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "# Report") {
		t.Fatalf("body = %q, want the artifact contents", body)
	}
}

// The in-app reader caps big files with a Range request, so a partial read has
// to actually come back partial.
func TestHandleFileHonoursRange(t *testing.T) {
	ts, _, runID := fileServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/file/"+runID+"/artifacts/report.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-7")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if len(body) != 8 {
		t.Fatalf("read %d bytes, want 8", len(body))
	}
	if got := res.Header.Get("Content-Range"); !strings.HasSuffix(got, "/20") {
		t.Fatalf("Content-Range = %q, want the total size suffix /20", got)
	}
}

func TestHandleFileRejectsTraversal(t *testing.T) {
	ts, runsDir, runID := fileServer(t)

	// A secret next to the run dir — the thing traversal would be after.
	secret := filepath.Join(runsDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"dot dot segment", "/file/" + runID + "/artifacts/../../secret.txt"},
		{"encoded dot dot", "/file/" + runID + "/artifacts/..%2f..%2fsecret.txt"},
		{"absolute-ish", "/file/" + runID + "//etc/passwd"},
		{"bad run id", "/file/..%2f..%2fetc/passwd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := http.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if strings.Contains(string(body), "TOP SECRET") || strings.Contains(string(body), "root:") {
				t.Fatalf("path %q leaked content outside the run dir (status %d)", tc.path, res.StatusCode)
			}
			if res.StatusCode == http.StatusOK {
				t.Fatalf("path %q returned 200, want a rejection", tc.path)
			}
		})
	}
}

// A symlink INSIDE the run dir is the traversal that survives lexical cleaning:
// the path never contains "..", so only resolving the link catches it.
func TestHandleFileRejectsSymlinkEscape(t *testing.T) {
	ts, runsDir, runID := fileServer(t)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE THE RUN"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(runsDir, runID, "artifacts", "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res, err := http.Get(ts.URL + "/file/" + runID + "/artifacts/escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %q)", res.StatusCode, body)
	}
	if strings.Contains(string(body), "OUTSIDE THE RUN") {
		t.Fatal("symlink served a file from outside the run directory")
	}
}

// ServeMux rewrites a `..` URL into `/file/{name}/` with an empty path. Left
// unchecked that resolves to <runsDir>/{name} — a sibling of the run, not part
// of it — so an empty path is a rejection, and a directory is never browsable.
func TestHandleFileRejectsRunDirItself(t *testing.T) {
	ts, runsDir, runID := fileServer(t)

	if err := os.WriteFile(filepath.Join(runsDir, "sibling.txt"), []byte("NOT THIS RUN"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/file/" + runID + "/",
		"/file/sibling.txt/",
		"/file/" + runID + "/artifacts/",
	} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Fatalf("path %q returned 200 (body %q), want a rejection", path, body)
		}
		if strings.Contains(string(body), "NOT THIS RUN") {
			t.Fatalf("path %q served a file outside the run", path)
		}
	}
}

func TestHandleFileMissingFileIs404(t *testing.T) {
	ts, _, runID := fileServer(t)

	res, err := http.Get(ts.URL + "/file/" + runID + "/artifacts/nope.md")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
