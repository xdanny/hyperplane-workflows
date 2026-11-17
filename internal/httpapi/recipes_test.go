package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// recipeSlots is a two-required/one-optional slot set, enough to exercise both
// halves of the validation.
var recipeSlots = []store.ContextSlot{
	{ID: "market", Label: "Market", Required: true},
	{ID: "audience", Label: "Audience", Required: true},
	{ID: "competitorsHint", Label: "Competitors you already know"},
}

var recipeCriteria = []store.Criterion{
	{ID: "sourced-competitor-set", Title: "Every competitor is real and sourced"},
	{ID: "differentiated-positioning", Title: "The positioning is differentiated"},
}

// seedRecipe uses real SDK transport with deterministic model responses.
func seedRecipe(t *testing.T) store.WorkflowTemplate {
	t.Helper()
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(findRepoRoot(t), "internal/codex/testdata/nodes"))
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping recipe integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("cannot connect to database: %v", err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tmpl, err := st.CreateTemplate(ctx, store.WorkflowTemplate{
		Name:         "recipe-http-test-" + time.Now().Format("150405.000000000"),
		Spec:         store.JSON(`{"kind":"nodes","instruction":"Research {slot:market} for {slot:audience}.","nodes":[{"id":"explore","title":"Explore","instruction":"Explore {slot:market}."},{"id":"review","title":"Review","instruction":"Summarize the options for {slot:audience}."}]}`),
		ContextSlots: recipeSlots,
		Criteria:     recipeCriteria,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return tmpl
}

func postInstantiate(t *testing.T, tsURL, id string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(tsURL+"/api/templates/"+id+"/instantiate", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST instantiate: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode instantiate response: %v", err)
	}
	return resp.StatusCode, out
}

// TestInstantiateRejectsMissingSlots asserts a recipe refuses to start when it
// does not know enough, and names exactly what is missing.
func TestInstantiateRejectsMissingSlots(t *testing.T) {
	tmpl := seedRecipe(t)
	ts := setupTestServer(t)

	status, body := postInstantiate(t, ts.URL, tmpl.ID, map[string]any{
		"slots": map[string]string{"market": "self-hosted time tracking"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", status, body)
	}
	missing, _ := body["missingSlots"].([]any)
	if len(missing) != 1 || missing[0] != "audience" {
		t.Fatalf("missingSlots = %v, want [audience]", body["missingSlots"])
	}
	if _, ok := body["draft"]; ok {
		t.Error("a rejected instantiate still created a draft")
	}
}

// TestInstantiateCarriesAutoRepairIntoTheLaunchRecord: the self-heal opt-in is
// made once, when the recipe is launched, and has to survive on the run itself —
// the run is what will later fail, and the flag is what decides whether anything
// happens when it does. Default OFF, because spending the one bounded repair is
// an irreversible choice nobody should make by accident.
func TestInstantiateCarriesAutoRepairIntoTheLaunchRecord(t *testing.T) {
	tmpl := seedRecipe(t)
	ts := setupTestServer(t)
	st := openStore(t)

	launch := func(autoRepair bool) store.Run {
		t.Helper()
		body := map[string]any{
			"slots": map[string]string{"market": "time tracking", "audience": "agencies"},
			"run":   true,
		}
		if autoRepair {
			body["autoRepair"] = true
		}
		status, out := postInstantiate(t, ts.URL, tmpl.ID, body)
		if status != http.StatusCreated {
			t.Fatalf("instantiate: %d (%v)", status, out)
		}
		runID, _ := out["runId"].(string)
		if runID == "" {
			t.Fatalf("no runId in response: %v", out)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), runID) })

		deadline := time.Now().Add(30 * time.Second)
		for {
			run, err := st.GetRun(context.Background(), runID)
			if err == nil && len(run.Options) > 0 && run.State != "running" {
				return run
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s never recorded its launch options", runID)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	autoRepair := func(run store.Run) bool {
		var options struct {
			AutoRepair bool `json:"autoRepair"`
		}
		if err := json.Unmarshal(run.Options, &options); err != nil {
			t.Fatal(err)
		}
		return options.AutoRepair
	}

	if got := launch(false); autoRepair(got) {
		t.Errorf("auto-repair must default to off, options = %s", got.Options)
	}
	if got := launch(true); !autoRepair(got) {
		t.Errorf("auto-repair did not reach the run's launch record, options = %s", got.Options)
	}
}

// TestListTemplatesCarriesSlotsAndCriteria asserts the wire shape the web
// composer and the CLI both read.
func TestListTemplatesCarriesSlotsAndCriteria(t *testing.T) {
	tmpl := seedRecipe(t)
	ts := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/templates")
	if err != nil {
		t.Fatalf("GET /api/templates: %v", err)
	}
	defer resp.Body.Close()
	var tmpls []store.WorkflowTemplate
	if err := json.NewDecoder(resp.Body).Decode(&tmpls); err != nil {
		t.Fatalf("decode templates: %v", err)
	}

	for _, got := range tmpls {
		if got.ID != tmpl.ID {
			continue
		}
		if len(got.ContextSlots) != len(recipeSlots) {
			t.Fatalf("contextSlots = %d, want %d", len(got.ContextSlots), len(recipeSlots))
		}
		if !got.ContextSlots[0].Required || got.ContextSlots[2].Required {
			t.Errorf("required flags did not survive the wire: %+v", got.ContextSlots)
		}
		if len(got.Criteria) != len(recipeCriteria) {
			t.Errorf("criteria = %d, want %d", len(got.Criteria), len(recipeCriteria))
		}
		return
	}
	t.Fatalf("template %s not present in GET /api/templates", tmpl.ID)
}

func TestTemplatePreparationRejectsLegacyGraphsAndLaunchOverrides(t *testing.T) {
	st := openStore(t)
	ts := setupTestServer(t)
	legacy, err := st.CreateTemplate(context.Background(), store.WorkflowTemplate{Name: "retired-template-" + projectSuffix(), Spec: store.JSON(`{"name":"old","nodes":[{"id":"run","kind":"shell","run":"touch forbidden"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	before := storedDraftSnapshot(t)
	for _, run := range []bool{false, true} {
		status, body := postInstantiate(t, ts.URL, legacy.ID, map[string]any{"run": run})
		if status != 400 {
			t.Fatalf("legacy graph accepted: %d %v", status, body)
		}
		if body["draft"] != nil || body["runId"] != nil {
			t.Fatal("legacy request created work")
		}
	}
	valid := seedRecipe(t)
	for _, option := range []map[string]any{{"dryRun": true}, {"goal": "replacement goal"}} {
		option["slots"] = map[string]string{"market": "example", "audience": "example"}
		status, body := postInstantiate(t, ts.URL, valid.ID, option)
		if status != 400 || body["runId"] != nil {
			t.Fatalf("legacy launch override accepted: %d %v", status, body)
		}
	}
	if string(before) != string(storedDraftSnapshot(t)) {
		t.Fatal("template preparation created graph drafts")
	}
}
