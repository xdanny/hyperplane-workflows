package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// openTestStore connects to the real DB if DATABASE_URL is set, otherwise
// returns (nil, skip). Tests that need a store call t.Skip via this helper.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("no DATABASE_URL — skipping DB test")
	}
	ctx := context.Background()
	st, err := openOnce(ctx, dsn)
	if err != nil {
		t.Skipf("DB unreachable: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// openOnce is package-level access to store.openOnce without importing it
// (same package — test file is in package orchestrate, not orchestrate_test).
// We replicate it here to keep the test hermetic.
func openOnce(ctx context.Context, dsn string) (*store.Store, error) {
	return store.Open(ctx, dsn)
}

// ---------------------------------------------------------------------------
// 1. DryRun=true → all items return state="dry-run", Done==0
// ---------------------------------------------------------------------------

func TestOrchestrate_DryRun(t *testing.T) {
	items := []BacklogItem{
		{Title: "build auth service"},
		{Title: "add CI pipeline"},
		{Title: "write API docs"},
	}

	result, err := Orchestrate(context.Background(), nil, OrchestrateConfig{
		BacklogItems: items,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if len(result.Items) != len(items) {
		t.Fatalf("expected %d item results, got %d", len(items), len(result.Items))
	}
	for _, it := range result.Items {
		if it.State != "dry-run" {
			t.Errorf("item %q: expected state=dry-run, got %q", it.Title, it.State)
		}
	}
	if result.Done != 0 {
		t.Errorf("DryRun: expected Done==0, got %d", result.Done)
	}
}

// ---------------------------------------------------------------------------
// 2. MaxItems cap
// ---------------------------------------------------------------------------

func TestOrchestrate_MaxItems(t *testing.T) {
	items := []BacklogItem{
		{Title: "item-1"},
		{Title: "item-2"},
		{Title: "item-3"},
		{Title: "item-4"},
		{Title: "item-5"},
	}
	result, err := Orchestrate(context.Background(), nil, OrchestrateConfig{
		BacklogItems: items,
		MaxItems:     2,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 results (MaxItems=2), got %d", len(result.Items))
	}
	if result.Items[0].Title != "item-1" {
		t.Errorf("expected first item to be item-1, got %q", result.Items[0].Title)
	}
	if result.Items[1].Title != "item-2" {
		t.Errorf("expected second item to be item-2, got %q", result.Items[1].Title)
	}
}

// ---------------------------------------------------------------------------
// 3. LoadBacklog from a temp JSON file
// ---------------------------------------------------------------------------

func TestLoadBacklog_File(t *testing.T) {
	payload := []BacklogItem{
		{Title: "migrate database", Detail: "run 0004 migration"},
		{Title: "write tests"},
	}
	data, _ := json.Marshal(payload)

	f, err := os.CreateTemp(t.TempDir(), "backlog-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()

	items, err := LoadBacklog(f.Name())
	if err != nil {
		t.Fatalf("LoadBacklog: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "migrate database" {
		t.Errorf("item[0].Title: want %q, got %q", "migrate database", items[0].Title)
	}
	if items[0].Detail != "run 0004 migration" {
		t.Errorf("item[0].Detail: want %q, got %q", "run 0004 migration", items[0].Detail)
	}
	if items[1].Title != "write tests" {
		t.Errorf("item[1].Title: want %q, got %q", "write tests", items[1].Title)
	}
}

// ---------------------------------------------------------------------------
// 4. LoadBacklogFromRunOutput — array-of-strings and array-of-objects
// ---------------------------------------------------------------------------

func TestLoadBacklogFromRunOutput_Strings(t *testing.T) {
	raw := []byte(`["build login","add metrics","deploy staging"]`)
	items, err := LoadBacklogFromRunOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	want := []string{"build login", "add metrics", "deploy staging"}
	for i, w := range want {
		if items[i].Title != w {
			t.Errorf("item[%d].Title: want %q, got %q", i, w, items[i].Title)
		}
		if items[i].Detail != "" {
			t.Errorf("item[%d].Detail should be empty, got %q", i, items[i].Detail)
		}
	}
}

func TestLoadBacklogFromRunOutput_Objects(t *testing.T) {
	raw := []byte(`[{"title":"build auth","detail":"OAuth2 + PKCE"},{"title":"add CI"}]`)
	items, err := LoadBacklogFromRunOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "build auth" {
		t.Errorf("item[0].Title: want %q, got %q", "build auth", items[0].Title)
	}
	if items[0].Detail != "OAuth2 + PKCE" {
		t.Errorf("item[0].Detail: want %q, got %q", "OAuth2 + PKCE", items[0].Detail)
	}
	if items[1].Title != "add CI" {
		t.Errorf("item[1].Title: want %q, got %q", "add CI", items[1].Title)
	}
}

func TestLoadBacklogFromRunOutput_Empty(t *testing.T) {
	_, err := LoadBacklogFromRunOutput([]byte{})
	if err == nil {
		t.Error("expected error on empty input")
	}
}

func TestLoadBacklogFromRunOutput_InvalidJSON(t *testing.T) {
	_, err := LoadBacklogFromRunOutput([]byte(`not json`))
	if err == nil {
		t.Error("expected error on invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// 5. StopOnFail — stub the runner; fail on item 2, assert loop stopped
// ---------------------------------------------------------------------------

func TestOrchestrate_StopOnFail(t *testing.T) {
	calls := 0
	result, err := Orchestrate(context.Background(), nil, OrchestrateConfig{
		BacklogItems: []BacklogItem{{Title: "one", Check: []string{"check"}}, {Title: "two", Check: []string{"check"}}, {Title: "three", Check: []string{"check"}}}, StopOnFail: true,
		Execute: func(context.Context, sdkruntime.Options, string, []string) (sdkruntime.Result, error) {
			calls++
			state := "passed"
			if calls == 2 {
				state = "failed"
			}
			return sdkruntime.Result{RunID: fmt.Sprint(calls), State: state}, nil
		},
	})
	if err != nil || calls != 2 || len(result.Items) != 2 || result.Done != 1 || result.Failed != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestOrchestrateRefusesIncompleteBacklogAndStopsOnPersistenceFailure(t *testing.T) {
	calls := 0
	cfg := OrchestrateConfig{BacklogItems: []BacklogItem{{Title: "one", Check: []string{"check"}}, {Title: "two"}}, Execute: func(context.Context, sdkruntime.Options, string, []string) (sdkruntime.Result, error) {
		calls++
		return sdkruntime.Result{State: "passed"}, nil
	}}
	if _, err := Orchestrate(context.Background(), nil, cfg); err == nil || calls != 0 {
		t.Fatal("incomplete backlog spent a model call")
	}
	cfg.BacklogItems[1].Check = []string{"check"}
	cfg.Progress = func(OrchestrateResult) error { return fmt.Errorf("database unavailable") }
	result, err := Orchestrate(context.Background(), nil, cfg)
	if err == nil || calls != 1 || len(result.Items) != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestOrchestrateValidatesEverySelectedTaskBeforeExecution(t *testing.T) {
	for _, check := range [][]string{{" "}, {"true", "bad\x00arg"}, {"true", strings.Repeat("x", 20001)}} {
		t.Run(fmt.Sprintf("check-%q", check[0]), func(t *testing.T) {
			calls := 0
			cfg := OrchestrateConfig{
				BacklogItems: []BacklogItem{{Title: "Valid first task", Check: []string{"true"}}, {Title: "Invalid later task", Check: check}},
				Execute: func(context.Context, sdkruntime.Options, string, []string) (sdkruntime.Result, error) {
					calls++
					return sdkruntime.Result{State: "passed"}, nil
				},
			}
			if _, err := Orchestrate(t.Context(), nil, cfg); err == nil || calls != 0 {
				t.Fatalf("invalid later task must stop admission before execution: calls=%d error=%v", calls, err)
			}
			cfg.MaxItems = 1
			if _, err := Orchestrate(t.Context(), nil, cfg); err != nil || calls != 1 {
				t.Fatalf("unselected later task must not block selected work: calls=%d error=%v", calls, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 6. DB-backed tests (skip when no DATABASE_URL)
// ---------------------------------------------------------------------------

func TestStore_OrchestrateRun_CRUD(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	id := fmt.Sprintf("test-orch-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM orchestrate_runs WHERE id=$1`, id)
	})
	_, err := st.CreateOrchestrateRun(ctx, store.CreateOrchestrateRunParams{
		ID:          id,
		BacklogSize: 5,
		MaxItems:    3,
	})
	if err != nil {
		t.Fatalf("CreateOrchestrateRun: %v", err)
	}

	rec, err := st.GetOrchestrateRun(ctx, id)
	if err != nil {
		t.Fatalf("GetOrchestrateRun: %v", err)
	}
	if rec.Status != "running" {
		t.Errorf("initial status: want running, got %q", rec.Status)
	}
	if rec.BacklogSize != 5 {
		t.Errorf("BacklogSize: want 5, got %d", rec.BacklogSize)
	}

	items := []OrchestrateItemResult{{Title: "t1", State: "done"}}
	itemsJSON, _ := json.Marshal(items)
	if err := st.UpdateOrchestrateRun(ctx, id, 1, 0, itemsJSON, "done"); err != nil {
		t.Fatalf("UpdateOrchestrateRun: %v", err)
	}

	rec2, err := st.GetOrchestrateRun(ctx, id)
	if err != nil {
		t.Fatalf("GetOrchestrateRun after update: %v", err)
	}
	if rec2.Status != "done" {
		t.Errorf("updated status: want done, got %q", rec2.Status)
	}
	if rec2.Done != 1 {
		t.Errorf("Done: want 1, got %d", rec2.Done)
	}
}
