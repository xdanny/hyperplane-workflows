package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/httpapi"
)

// artifactEntryWire mirrors the unexported artifactEntry JSON shape returned
// by GET /api/runs/{id}/artifacts.
type artifactEntryWire struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// artifactServer builds a Server backed only by a temp runs dir, with one run
// whose artifacts/ directory already exists (empty). No database needed:
// GET /api/runs/{id}/artifacts and GET /file/... never touch the store — see
// files_test.go's fileServer for the same pattern.
func artifactServer(t *testing.T) (ts *httptest.Server, runsDir, runID, artDir string) {
	t.Helper()

	runsDir = t.TempDir()
	runID = "20260101-130000.000-artifacts"
	artDir = filepath.Join(runsDir, runID, "artifacts")
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Addr:         "127.0.0.1:0",
		PlatformRoot: t.TempDir(),
		TargetRoot:   t.TempDir(),
		RunsDir:      runsDir,
	}
	s := httptest.NewServer(httpapi.New(cfg, nil).Handler())
	t.Cleanup(s.Close)
	return s, runsDir, runID, artDir
}

// writeArtifact writes content at <artDir>/<relPath>, creating any parent
// directories relPath implies.
func writeArtifact(t *testing.T, artDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(artDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listArtifacts(t *testing.T, ts *httptest.Server, runID string) []artifactEntryWire {
	t.Helper()
	res, err := http.Get(ts.URL + "/api/runs/" + runID + "/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, want 200 (body %s)", res.StatusCode, body)
	}
	var out []artifactEntryWire
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// Nested artifacts up to 3 directory levels deep must be listed, with
// slash-separated relative names, correct sizes and URLs, sorted by name.
func TestHandleListArtifactsNested(t *testing.T) {
	ts, _, runID, artDir := artifactServer(t)

	writeArtifact(t, artDir, "top.txt", "0123456789")   // depth 0, 10 bytes
	writeArtifact(t, artDir, "a/one.txt", "aa")         // depth 1
	writeArtifact(t, artDir, "a/b/two.txt", "bbb")      // depth 2
	writeArtifact(t, artDir, "a/b/c/three.txt", "cccc") // depth 3, the deepest allowed
	writeArtifact(t, artDir, "z/four.txt", "d")

	got := listArtifacts(t, ts, runID)

	want := []artifactEntryWire{
		{Name: "a/b/c/three.txt", Size: 4, URL: "/file/" + runID + "/artifacts/a/b/c/three.txt"},
		{Name: "a/b/two.txt", Size: 3, URL: "/file/" + runID + "/artifacts/a/b/two.txt"},
		{Name: "a/one.txt", Size: 2, URL: "/file/" + runID + "/artifacts/a/one.txt"},
		{Name: "top.txt", Size: 10, URL: "/file/" + runID + "/artifacts/top.txt"},
		{Name: "z/four.txt", Size: 1, URL: "/file/" + runID + "/artifacts/z/four.txt"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A file nested deeper than 3 directory levels below artifacts/ is skipped
// silently — it must not appear, and must not turn into an error for the
// files that ARE within bounds.
func TestHandleListArtifactsExcludesBeyondMaxDepth(t *testing.T) {
	ts, _, runID, artDir := artifactServer(t)

	writeArtifact(t, artDir, "a/b/c/allowed.txt", "ok")      // depth 3: kept
	writeArtifact(t, artDir, "a/b/c/d/too-deep.txt", "nope") // depth 4: dropped
	writeArtifact(t, artDir, "a/b/c/d/e/f/way-too-deep.txt", "nope2")

	got := listArtifacts(t, ts, runID)

	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(got), got)
	}
	if got[0].Name != "a/b/c/allowed.txt" {
		t.Fatalf("Name = %q, want %q", got[0].Name, "a/b/c/allowed.txt")
	}
}

// A symlink inside artifacts/ pointing outside the run dir — to a file, or
// to a directory — must never be listed or followed. This is the same
// traversal shape files.go guards against on GET /file/…, just reached
// through the listing instead.
func TestHandleListArtifactsSkipsSymlinkEscape(t *testing.T) {
	ts, _, runID, artDir := artifactServer(t)

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideSubdir := filepath.Join(outsideDir, "sub")
	if err := os.MkdirAll(outsideSubdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideSubdir, "in-sub.txt"), []byte("ALSO SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A legitimate file, so we can tell "skipped the symlinks" apart from
	// "listing broke entirely".
	writeArtifact(t, artDir, "legit.txt", "fine")

	if err := os.Symlink(outsideFile, filepath.Join(artDir, "escape-file.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outsideSubdir, filepath.Join(artDir, "escape-dir")); err != nil {
		t.Fatal(err)
	}

	got := listArtifacts(t, ts, runID)

	if len(got) != 1 || got[0].Name != "legit.txt" {
		t.Fatalf("got %+v, want only legit.txt (symlinked file/dir must not be listed or followed)", got)
	}
}

// Missing or empty artifacts/ is 200 [], not an error.
func TestHandleListArtifactsEmptyOrMissing(t *testing.T) {
	ts, runsDir, runID, artDir := artifactServer(t)

	got := listArtifacts(t, ts, runID)
	if len(got) != 0 {
		t.Fatalf("empty dir: got %+v, want []", got)
	}

	// Now remove the directory entirely and use a fresh run id that never had
	// one, to also cover the "artifacts/ was never created" path.
	if err := os.RemoveAll(artDir); err != nil {
		t.Fatal(err)
	}
	got = listArtifacts(t, ts, runID)
	if len(got) != 0 {
		t.Fatalf("removed dir: got %+v, want []", got)
	}

	const otherRunID = "20260101-140000.000-no-artifacts"
	if err := os.MkdirAll(filepath.Join(runsDir, otherRunID), 0o755); err != nil {
		t.Fatal(err)
	}
	got = listArtifacts(t, ts, otherRunID)
	if len(got) != 0 {
		t.Fatalf("never-created dir: got %+v, want []", got)
	}
}

// A nested artifact's URL, taken straight from the listing, must actually
// serve the file's content through GET /file/….
func TestHandleListArtifactsURLIsFetchable(t *testing.T) {
	ts, _, runID, artDir := artifactServer(t)

	writeArtifact(t, artDir, "audit/rules/foo.sql", "SELECT 1;\n")

	got := listArtifacts(t, ts, runID)
	if len(got) != 1 || got[0].Name != "audit/rules/foo.sql" {
		t.Fatalf("got %+v, want a single audit/rules/foo.sql entry", got)
	}

	res, err := http.Get(ts.URL + got[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200", got[0].URL, res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "SELECT 1;\n" {
		t.Fatalf("body = %q, want the artifact contents", body)
	}
}
