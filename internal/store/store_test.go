package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

// testStore opens a connection to the test Postgres. It skips the test (rather
// than failing) when no database is reachable, so `go test ./...` stays green on
// machines without a local db; CI and `make test` provide DATABASE_URL.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping store tests")
	}
	if err := dbtest.GuardTestDSN(dsn); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	st, err := openOnce(ctx, dsn)
	if err != nil {
		t.Fatalf("no postgres reachable (%v); skipping store tests", err)
	}
	if err := CheckSchemaProvenance(ctx, st.Pool, dsn); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestRunLifecycle(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	runID := "test-run-" + uniq()
	t.Cleanup(func() { _ = st.DeleteRun(ctx, runID) })

	if err := st.CreateRun(ctx, Run{
		RunID: runID, DAG: "hello", State: "running", Type: "automation",
		StartedAt: time.Now(), Options: JSON(`{"dryRun":false}`),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := st.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.DAG != "hello" || got.State != "running" {
		t.Fatalf("unexpected run: %+v", got)
	}

	// Node upsert + update.
	if err := st.UpsertNode(ctx, Node{RunID: runID, NodeID: "n1", Kind: "agent", State: "running", Attempt: 1}); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	score := 0.91
	if err := st.UpsertNode(ctx, Node{RunID: runID, NodeID: "n1", Kind: "agent", State: "passed", Attempt: 1, Score: &score, Output: JSON(`{"ok":true}`)}); err != nil {
		t.Fatalf("update node: %v", err)
	}
	nodes, err := st.ListNodes(ctx, runID)
	if err != nil || len(nodes) != 1 || nodes[0].State != "passed" {
		t.Fatalf("list nodes: %v %+v", err, nodes)
	}
	if nodes[0].Score == nil || *nodes[0].Score != score {
		t.Fatalf("node score not persisted: %+v", nodes[0])
	}

	// Events get monotonic seq.
	s1, err := st.AppendEvent(ctx, Event{RunID: runID, Type: "run.start"})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	s2, _ := st.AppendEvent(ctx, Event{RunID: runID, Type: "node.start", Node: "n1"})
	if s1 != 1 || s2 != 2 {
		t.Fatalf("expected seq 1,2 got %d,%d", s1, s2)
	}
	evs, err := st.ListEvents(ctx, runID, 1, 10)
	if err != nil || len(evs) != 1 || evs[0].Node != "n1" {
		t.Fatalf("list events since 1: %v %+v", err, evs)
	}

	// Terminal state sets ended_at.
	if err := st.SetRunState(ctx, runID, "passed"); err != nil {
		t.Fatalf("set state: %v", err)
	}
	got, _ = st.GetRun(ctx, runID)
	if got.State != "passed" || got.EndedAt.IsZero() {
		t.Fatalf("terminal state not applied: %+v", got)
	}
}

func TestRecordsByIDAndPendingState(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "apr-" + uniq()
	gk := "wf:gate1:" + uniq()
	t.Cleanup(func() { _ = st.DeleteRecord(ctx, "approvals", id) })

	if err := st.UpsertRecord(ctx, Record{Type: "approvals", ID: id, State: "pending", GateKey: gk, Kind: "approval"}); err != nil {
		t.Fatalf("upsert record: %v", err)
	}
	found, err := st.GetRecord(ctx, "approvals", id)
	if err != nil || found.ID != id || found.GateKey != gk {
		t.Fatalf("get record lost fields: %v %+v", err, found)
	}
	pending, err := st.ListRecords(ctx, "approvals", "pending")
	if err != nil || len(pending) == 0 {
		t.Fatalf("list pending: %v %d", err, len(pending))
	}
}

// TestClaimRecordIsInsertOnly pins the property automatic learning depends on:
// the first writer wins and every later one is told the work is taken. An
// upsert here would let one accepted delivery fire two billed extractions.
func TestClaimRecordIsInsertOnly(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "claim-" + uniq()
	t.Cleanup(func() { _ = st.DeleteRecord(ctx, "learning", id) })

	first := Record{Type: "learning", ID: id, State: "claimed", RunID: "run-1", Kind: "skill-extraction"}
	if err := st.ClaimRecord(ctx, first); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	second := Record{Type: "learning", ID: id, State: "claimed", RunID: "run-2", Kind: "skill-extraction"}
	if err := st.ClaimRecord(ctx, second); !errors.Is(err, ErrExists) {
		t.Fatalf("second claim: got %v, want ErrExists", err)
	}

	got, err := st.GetRecord(ctx, "learning", id)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if got.RunID != "run-1" {
		t.Errorf("run id = %q — a refused claim must not overwrite the winner", got.RunID)
	}

	// The claimant can still record the OUTCOME on its own claim.
	if err := st.UpsertRecord(ctx, Record{Type: "learning", ID: id, State: "drafted", RunID: "run-1"}); err != nil {
		t.Fatalf("update own claim: %v", err)
	}
	if got, _ := st.GetRecord(ctx, "learning", id); got.State != "drafted" {
		t.Errorf("state = %q, want drafted", got.State)
	}
}

func TestSkillsSearch(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "skill-test-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM skills WHERE id=$1`, id) })

	if err := st.UpsertSkill(ctx, Skill{
		ID: id, Name: "Liquid Glass Art Director",
		Description: "Art-direct a premium iOS Liquid Glass UI",
		Category:    "design-brand", Instruction: "...",
	}); err != nil {
		t.Fatalf("upsert skill: %v", err)
	}
	// Generous limit: the shared dev DB may hold the full seeded KB (incl. a real
	// liquid-glass-art-director), so the upserted skill need not rank top-10 — only
	// be present among the matches.
	res, err := st.SearchSkills(ctx, "liquid glass", 100)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var hit bool
	for _, s := range res {
		if s.ID == id {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected to find seeded skill in search results (%d results)", len(res))
	}
}

func TestPreferences(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	skill := "pref-test-" + uniq()
	cands, _ := json.Marshal([]map[string]any{{"id": "a"}, {"id": "b"}})
	id, err := st.AppendPreference(ctx, Preference{SkillID: skill, Source: "human-pick", WinnerID: "a", Candidates: cands})
	if err != nil || id == 0 {
		t.Fatalf("append preference: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM preferences WHERE skill_id=$1`, skill) })
	prefs, err := st.ListPreferences(ctx, skill)
	if err != nil || len(prefs) != 1 || prefs[0].WinnerID != "a" {
		t.Fatalf("list preferences: %v %+v", err, prefs)
	}
}

var uniqCounter int

// uniq returns a process-unique suffix without relying on time-based randomness.
func uniq() string {
	uniqCounter++
	return time.Now().Format("150405.000000") + "-" + itoa(uniqCounter)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
