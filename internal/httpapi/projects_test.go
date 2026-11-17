package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// projectSuffix keeps ids unique across a package run without a shared counter.
func projectSuffix() string { return time.Now().Format("150405.000000000") }

// doJSON issues a request with an optional JSON body and returns the status
// plus the raw response.
func doJSON(t *testing.T, method, url string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// listProjects reads GET /api/projects and returns the entry for one id.
func listProjects(t *testing.T, base, id string) (store.ProjectCount, bool) {
	t.Helper()
	status, raw := doJSON(t, http.MethodGet, base+"/api/projects", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/projects: expected 200, got %d: %s", status, raw)
	}
	var list []store.ProjectCount
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode projects: %v (%s)", err, raw)
	}
	for _, pc := range list {
		if pc.Project == id {
			return pc, true
		}
	}
	return store.ProjectCount{}, false
}

// TestProjectsAPICRUD walks POST → GET list → GET one → PATCH, and asserts the
// insert-only conflict and the not-found paths.
func TestProjectsAPICRUD(t *testing.T) {
	ts := setupTestServer(t)
	id := "api-proj-" + projectSuffix()

	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/projects",
		map[string]any{"id": id, "name": "API Project", "summary": "made over HTTP"})
	if status != http.StatusCreated {
		t.Fatalf("POST /api/projects: expected 201, got %d: %s", status, raw)
	}
	var created store.Project
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode created project: %v (%s)", err, raw)
	}
	if created.ID != id || created.Name != "API Project" || created.Status != "active" {
		t.Fatalf("unexpected created project: %+v", created)
	}

	// Insert-only: the same id again is a conflict, not a silent overwrite.
	if status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/projects",
		map[string]any{"id": id, "name": "Clobber"}); status != http.StatusConflict {
		t.Fatalf("duplicate POST: expected 409, got %d: %s", status, raw)
	}

	// A project with nothing tagged with it still appears in the list.
	pc, ok := listProjects(t, ts.URL, id)
	if !ok {
		t.Fatalf("newly created project missing from GET /api/projects")
	}
	if !pc.Registered || pc.Count != 0 || pc.Name != "API Project" {
		t.Fatalf("unexpected list entry: %+v", pc)
	}

	// PATCH leaves absent fields alone.
	status, raw = doJSON(t, http.MethodPatch, ts.URL+"/api/projects/"+id,
		map[string]any{"summary": "edited over HTTP"})
	if status != http.StatusOK {
		t.Fatalf("PATCH: expected 200, got %d: %s", status, raw)
	}
	var patched store.Project
	if err := json.Unmarshal(raw, &patched); err != nil {
		t.Fatalf("decode patched project: %v (%s)", err, raw)
	}
	if patched.Summary != "edited over HTTP" || patched.Name != "API Project" {
		t.Fatalf("PATCH did not apply a partial edit: %+v", patched)
	}

	// GET one returns the detail shape, with workflows always an array.
	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET one: expected 200, got %d: %s", status, raw)
	}
	var detail store.ProjectDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode project detail: %v (%s)", err, raw)
	}
	if detail.Project != id || detail.Workflows == nil {
		t.Fatalf("unexpected detail: %+v (raw %s)", detail, raw)
	}

	// Unknown ids are 404 on both read and write.
	missing := "no-such-project-" + projectSuffix()
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+missing, nil); status != http.StatusNotFound {
		t.Fatalf("GET unknown project: expected 404, got %d", status)
	}
	if status, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/projects/"+missing,
		map[string]any{"name": "x"}); status != http.StatusNotFound {
		t.Fatalf("PATCH unknown project: expected 404, got %d", status)
	}

	// A body with no usable id is a client error, not a 500.
	if status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/projects", map[string]any{}); status != http.StatusBadRequest {
		t.Fatalf("POST with no id: expected 400, got %d", status)
	}
	// A non-boolean includeTest is reported rather than silently treated false.
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/projects?includeTest=maybe", nil); status != http.StatusBadRequest {
		t.Fatalf("GET ?includeTest=maybe: expected 400, got %d", status)
	}
}

// TestProjectsAPIAutoRegistersFromRun asserts the end-to-end auto-register
// path: triggering a run with a project tag makes the project appear on
// GET /api/projects with its run counted and the SDK recipe listed as its workflow.
func TestProjectsAPIAutoRegistersFromRun(t *testing.T) {
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/ideator"))
	t.Setenv("SDK_FIXTURE_MODE", "ordinary")
	ts := setupTestServer(t)
	proj := "auto-api-" + projectSuffix()

	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/run",
		map[string]any{"workflow": "app-ideator", "project": proj})
	if status != http.StatusAccepted && status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /api/run: unexpected status %d: %s", status, raw)
	}

	// The run is created before it is scheduled, so the project row appears
	// promptly; poll briefly rather than racing the scheduler.
	var pc store.ProjectCount
	var ok bool
	for i := 0; i < 40; i++ {
		if pc, ok = listProjects(t, ts.URL, proj); ok && pc.Runs > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("run's project %q was never auto-registered", proj)
	}
	if !pc.Registered || pc.Runs < 1 || pc.Count < 1 {
		t.Fatalf("unexpected auto-registered entry: %+v", pc)
	}

	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+proj, nil)
	if status != http.StatusOK {
		t.Fatalf("GET project detail: expected 200, got %d: %s", status, raw)
	}
	var detail store.ProjectDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	found := false
	for _, wf := range detail.Workflows {
		if wf.DAG == "app-ideator" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'hello' among the project's workflows, got %s", mustJSON(t, detail.Workflows))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}

// TestProjectDeclarationsAPI walks the surface a form actually uses: create
// with declarations, read them back on both the list and the detail endpoint,
// PATCH one group, and confirm the machine's narrative is not something a
// client can type.
func TestProjectDeclarationsAPI(t *testing.T) {
	ts := setupTestServer(t)
	id := "api-decl-" + projectSuffix()

	declared := map[string]any{
		"purpose": map[string]any{
			"owner":     "Priya, analytics lead",
			"doneMeans": "The Q1 scorecard is accepted.",
		},
		"datasets": []map[string]any{{
			"name":         "NYC yellow-cab drops",
			"location":     "data/yellow_tripdata_2025-*.csv",
			"format":       "csv",
			"grain":        "one row per completed trip",
			"naturalKey":   "none",
			"keyCandidate": []string{"VendorID", "tpep_pickup_datetime"},
			"safeUse":      "aggregate reporting only",
		}},
		"environment": map[string]any{"where": "a local workspace with duckdb"},
		// A group this build has no type for. Two more personas will add real
		// ones; if an ordinary edit can delete them, the document is not
		// extensible.
		"kpis": []map[string]any{{"id": "on-time", "target": 0.99}},
	}

	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"id": id, "name": "Declared", "summary": "What this project is for.",
		"declarations": declared,
		// Machine-owned, and offered here on purpose: the API must refuse it.
		"narrative": "we built everything already",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /api/projects: expected 201, got %d: %s", status, raw)
	}
	var created store.Project
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if created.Narrative != "" {
		t.Errorf("a client set the machine-written narrative on create: %q", created.Narrative)
	}
	if created.Summary != "What this project is for." {
		t.Errorf("summary = %q", created.Summary)
	}

	// The detail endpoint carries the declarations AND what they can prefill.
	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/projects/%s: expected 200, got %d: %s", id, status, raw)
	}
	var detail store.ProjectDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode detail: %v (%s)", err, raw)
	}
	if detail.Summary != "What this project is for." || detail.Narrative != "" {
		t.Errorf("detail carries summary=%q narrative=%q — both halves must be readable and distinct",
			detail.Summary, detail.Narrative)
	}
	// Marcus M-16 residual (2026-09-04): dataset keys are recipe-scoped;
	// the unscoped API view must not offer them to recipes with another grain.
	if p, ok := detail.SlotDefaults["keyColumns"]; ok && (p.Value != "" || p.Source != "") {
		t.Errorf("unscoped keyColumns slot default = %+v, want absent or empty", p)
	}
	if p, ok := store.LookupPrefill(detail.SlotDefaults, "data-quality-audit", "keyColumns"); !ok || p.Value != "none, VendorID, tpep_pickup_datetime" {
		t.Errorf("data-quality-audit keyColumns slot default = %+v, %t; want the `none, <candidates>` form", p, ok)
	}
	if p, ok := store.LookupPrefill(detail.SlotDefaults, "data-pipeline", "keyColumns"); ok {
		t.Errorf("data-pipeline inherited dataset keyColumns = %+v, want nothing", p)
	}
	// Sofia S-12(c): even one confidentiality source is labelled so its scope
	// is explicit; when both sources exist they remain separate, ordered lines.
	if got := detail.SlotDefaults["safeUse"].Value; got != "From the data's safe use: aggregate reporting only" {
		t.Errorf("safeUse slot default = %q", got)
	}
	if detail.SlotDefaults["environment"].Source == "" {
		t.Error("an inherited value arrived with no declaration path to attribute it to")
	}

	// The list surface carries them too — the project card reads that one.
	pc, ok := listProjects(t, ts.URL, id)
	if !ok {
		t.Fatal("the project is missing from GET /api/projects")
	}
	if !strings.Contains(string(pc.Declarations), "yellow_tripdata") {
		t.Errorf("the list row carries no declarations: %s", pc.Declarations)
	}

	status, raw = doJSON(t, http.MethodPatch, ts.URL+"/api/projects/"+id, map[string]any{
		"declarations": map[string]any{
			"purpose": map[string]any{
				"constraints": "never expose rider-level records",
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH purpose constraints: expected 200, got %d: %s", status, raw)
	}
	status, raw = doJSON(t, http.MethodGet, ts.URL+"/api/projects/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET project with both confidentiality sources: expected 200, got %d: %s", status, raw)
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode detail with both confidentiality sources: %v (%s)", err, raw)
	}
	wantSafeUse := "From this project's constraints: never expose rider-level records\n" +
		"From the data's safe use: aggregate reporting only"
	if got := detail.SlotDefaults["safeUse"].Value; got != wantSafeUse {
		t.Errorf("safeUse with project and data declarations = %q, want %q", got, wantSafeUse)
	}

	// PATCH one group. Everything else — including the group this build has
	// never heard of — survives.
	status, raw = doJSON(t, http.MethodPatch, ts.URL+"/api/projects/"+id, map[string]any{
		"declarations": map[string]any{
			"environment": map[string]any{"where": "a CI runner", "runtimeBudgetDefault": "inside 10 minutes"},
		},
		"narrative": "still not allowed",
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH: expected 200, got %d: %s", status, raw)
	}
	var patched store.Project
	if err := json.Unmarshal(raw, &patched); err != nil {
		t.Fatalf("decode patch: %v (%s)", err, raw)
	}
	if patched.Narrative != "" {
		t.Errorf("a client typed the machine-written narrative through PATCH: %q", patched.Narrative)
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(patched.Declarations, &groups); err != nil {
		t.Fatalf("declarations are not an object: %v (%s)", err, patched.Declarations)
	}
	for _, g := range []string{"purpose", "datasets", "environment", "kpis"} {
		if _, ok := groups[g]; !ok {
			t.Errorf("group %q was lost by an edit that did not mention it: %s", g, patched.Declarations)
		}
	}
	if !strings.Contains(string(groups["environment"]), "CI runner") {
		t.Errorf("the patched group was not replaced: %s", groups["environment"])
	}
	if patched.Summary != "What this project is for." {
		t.Errorf("a declarations edit disturbed the owner's description: %q", patched.Summary)
	}

	// A declaration document that is not an object is refused rather than
	// stored — a form bug must not be able to empty a project's declarations.
	status, raw = doJSON(t, http.MethodPatch, ts.URL+"/api/projects/"+id,
		map[string]any{"declarations": []int{1, 2, 3}})
	if status == http.StatusOK {
		t.Errorf("a non-object declarations document was accepted: %s", raw)
	}
}
