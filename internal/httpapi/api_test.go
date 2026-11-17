package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/httpapi"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// setupTestServer creates a Server backed by a real Postgres database, applies
// migrations, and returns an *httptest.Server. It skips if DATABASE_URL is unset
// or the database is unreachable.
func setupTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	_, ts := newTestServer(t, nil)
	return ts
}

// newTestServer is setupTestServer plus the *httpapi.Server itself, and a hook
// to adjust the config before it is built.
//
// The server handle is what lets a test reach the exported test-only seams
// (SetBriefEngine, DrainRuns); the config hook is for the switches a handler
// reads off it — HYPERPLANE_BRIEFS, chiefly, which is off in the default test
// config precisely so nothing bills an engine by accident.
func newTestServer(t *testing.T, tweak func(*config.Config)) (*httpapi.Server, *httptest.Server) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("cannot connect to database: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Find the SDK fixtures and application resources.
	// api_test.go lives at internal/httpapi/api_test.go; go up two levels.
	repoRoot := findRepoRoot(t)

	cfg := config.Config{
		Addr:         "127.0.0.1:0",
		DatabaseURL:  dbURL,
		PlatformRoot: repoRoot,
		// TargetRoot must NOT be the real checkout: runs that pass trigger the
		// knowledge writer, which git-commits into whatever repo owns TargetRoot.
		TargetRoot: t.TempDir(),
		RunsDir:    t.TempDir(),
	}

	if tweak != nil {
		tweak(&cfg)
	}

	srv := httpapi.New(cfg, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// POST /api/run launches SDK execution in a goroutine that outlives the HTTP
	// request, and it writes into RunsDir/TargetRoot — the t.TempDir() values
	// above. Registered after them so it runs first: every run goroutine must
	// be gone before Go's cleanup removes those directories, or RemoveAll races
	// the worker's writes and fails with "directory not empty".
	t.Cleanup(func() {
		if !srv.DrainRuns(90 * time.Second) {
			t.Error("SDK runs still active after 90s; TempDir cleanup may race")
		}
	})
	return srv, ts
}

// findRepoRoot walks up from the test binary's working dir until it finds go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dir[:len(dir)-len("/"+lastSegment(dir))]
		if parent == dir {
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// TestHealth asserts GET /api/health returns 200.
func TestHealth(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected ok=true, got %v", body)
	}
}

func TestHealthWithBrowserFixtureDoesNotInvokeModel(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "apps/web/e2e-live/fixtures/sdk-agent"))
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SDK_FIXTURE_BROWSER_CALLS", calls)
	ts := setupTestServer(t)
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		OK                 bool
		Codex, CodexDetail string
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !body.OK || body.Codex != "ok" {
		t.Fatalf("fixture installation unavailable: %+v", body)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("health invoked model fixture: %v", err)
	}
}

func TestHealthReportsSDKUnavailableWithoutExposingOverride(t *testing.T) {
	unavailable := filepath.Join(t.TempDir(), "private-override-name")
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", unavailable)
	ts := setupTestServer(t)
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		OK                 bool
		Codex, CodexDetail string
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OK || body.Codex != "unavailable" || strings.Contains(body.CodexDetail, unavailable) {
		t.Fatalf("wrong unavailable status: %+v", body)
	}
}
