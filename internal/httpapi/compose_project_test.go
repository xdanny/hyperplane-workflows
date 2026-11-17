package httpapi_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Request validation is covered here; sdk_plan_test.go covers successful and
// rejected proposals through the real SDK with explicit fixture model output.

// TestComposeRejectsUnsafeProject asserts a project that could not be a path
// segment is refused up front, rather than being auto-registered into a row
// nobody can then open.
func TestComposeRejectsUnsafeProject(t *testing.T) {
	ts := setupTestServer(t)

	for _, bad := range []string{"../etc", "acme/launch", `acme"?`} {
		status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/compose",
			map[string]any{"brief": "plan something", "project": bad})
		if status != http.StatusBadRequest {
			t.Fatalf("POST /api/compose project=%q: expected 400, got %d: %s", bad, status, raw)
		}
	}

	// Nothing was registered on the way to the rejection.
	if _, ok := listProjects(t, ts.URL, "../etc"); ok {
		t.Fatal("a rejected project tag was still registered")
	}
}

// TestComposeRequiresBrief keeps the project field from becoming a way to call
// the orchestrator with nothing to plan.
func TestComposeRequiresBrief(t *testing.T) {
	ts := setupTestServer(t)
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/compose",
		map[string]any{"project": "some-project"})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /api/compose with no brief: expected 400, got %d: %s", status, raw)
	}
}

// TestComposeRefusesPlaceholderBrief is Q-25's server half: a starter brief
// still full of "[replace with …]" must be refused BEFORE the project is
// registered and before a single billed second of planning.
func TestComposeRefusesPlaceholderBrief(t *testing.T) {
	ts := setupTestServer(t)
	project := "placeholder-scope-" + projectSuffix()

	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/compose", map[string]any{
		"brief": "Audit [replace with the dataset] at [replace with the grain] " +
			"and tell me [REPLACE WITH what good looks like].",
		"project": project,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unfilled brief, got %d: %s", status, raw)
	}
	var body struct {
		Error        string `json:"error"`
		Placeholders int    `json:"placeholders"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if body.Placeholders != 3 {
		t.Errorf("placeholders = %d, want 3", body.Placeholders)
	}
	for _, want := range []string{"3 '[replace with", "before requesting steps"} {
		if !strings.Contains(body.Error, want) {
			t.Errorf("error %q missing %q", body.Error, want)
		}
	}
	// The refusal happens before EnsureProject, so nothing was registered.
	if _, ok := listProjects(t, ts.URL, project); ok {
		t.Error("a refused brief still registered its project")
	}
}

// Preparing an SDK template carries project scope; launching registers its run.
func TestInstantiateCarriesProjectToSDKWorkflowAndRun(t *testing.T) {
	tmpl := seedRecipe(t)
	ts := setupTestServer(t)
	project := "recipe-scope-" + projectSuffix()

	status, body := postInstantiate(t, ts.URL, tmpl.ID, map[string]any{
		"slots": map[string]string{
			"market":   "self-hosted time tracking",
			"audience": "agency owners",
		},
		"run":     true,
		"project": project,
	})
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%v)", status, body)
	}
	if body["project"] != project {
		t.Errorf("response project = %v, want %q", body["project"], project)
	}

	// The prepared workflow carries the same scope as its launch.
	workflow, _ := body["workflow"].(map[string]any)
	if workflow == nil {
		t.Fatalf("no SDK workflow in response: %v", body)
	}
	if workflow["project"] != project {
		t.Errorf("workflow.project = %v, want %q", workflow["project"], project)
	}

	runID, _ := body["runId"].(string)
	if runID == "" {
		t.Fatalf("no runId in response: %v", body)
	}

	// The run row carries it too — the run is created inside Trigger's
	// goroutine, so poll rather than race the scheduler.
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, raw := doJSON(t, http.MethodGet, ts.URL+"/api/runs/"+runID, nil)
		if status == http.StatusOK {
			var payload struct {
				Run store.Run `json:"run"`
			}
			if err := json.Unmarshal(raw, &payload); err == nil && payload.Run.RunID != "" {
				if payload.Run.Project != project {
					t.Fatalf("run.project = %q, want %q", payload.Run.Project, project)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never appeared with its project", runID)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// And the project is on the Projects surface, counted.
	var pc store.ProjectCount
	var ok bool
	for i := 0; i < 40; i++ {
		if pc, ok = listProjects(t, ts.URL, project); ok && pc.Runs > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("project %q was never registered by the instantiate", project)
	}
	if !pc.Registered || pc.Runs < 1 {
		t.Fatalf("unexpected project entry after instantiate: %+v", pc)
	}
}

// TestInstantiateRejectsUnsafeProject mirrors the compose guard on the
// deterministic door.
func TestInstantiateRejectsUnsafeProject(t *testing.T) {
	tmpl := seedRecipe(t)
	ts := setupTestServer(t)

	status, body := postInstantiate(t, ts.URL, tmpl.ID, map[string]any{
		"slots": map[string]string{
			"market":   "self-hosted time tracking",
			"audience": "agency owners",
		},
		"project": "../escape",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unsafe project, got %d (%v)", status, body)
	}
	if _, hasDraft := body["draft"]; hasDraft {
		t.Error("a rejected project still created a draft")
	}
}

// TestUnscopedBucketIsNotAPickableProject pins what the new project pickers
// rely on: the empty-tag row comes back named (never blank) and never
// registered, so "offer every registered project" cannot accidentally offer
// the unscoped bucket as somewhere to put work.
func TestUnscopedBucketIsNotAPickableProject(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/ideator"))
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	ts := setupTestServer(t)

	// An untagged run guarantees the bucket exists.
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run",
		map[string]any{"workflow": "app-ideator"})
	if status != http.StatusAccepted && status != http.StatusOK {
		t.Fatalf("POST /api/run: unexpected status %d: %s", status, raw)
	}

	var bucket store.ProjectCount
	var ok bool
	for i := 0; i < 40; i++ {
		if bucket, ok = listProjects(t, ts.URL, ""); ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatal("the unscoped bucket never appeared in GET /api/projects")
	}
	if bucket.Name != store.UnscopedProjectName {
		t.Errorf("unscoped bucket name = %q, want %q", bucket.Name, store.UnscopedProjectName)
	}
	if bucket.Registered {
		t.Error("the unscoped bucket must not report itself as a registered project")
	}
}
