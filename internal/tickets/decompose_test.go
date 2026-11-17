package tickets_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
	"github.com/xdanny/hyperplane-workflows/internal/tickets"
)

// ── Fake store ────────────────────────────────────────────────────────────────

type fakeDecomposeStore struct {
	mu    sync.Mutex
	lanes map[string]*store.Lane
	tix   map[string]*store.Ticket
	runs  map[string]store.Run // key: runID
	// insertion-ordered ticket IDs so we can check order later.
	order []string
}

func newFakeDecomposeStore() *fakeDecomposeStore {
	return &fakeDecomposeStore{
		lanes: make(map[string]*store.Lane),
		tix:   make(map[string]*store.Ticket),
		runs:  make(map[string]store.Run),
	}
}

func (f *fakeDecomposeStore) CreateLaneWithTickets(_ context.Context, lane *store.Lane, records []store.Ticket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *lane
	f.lanes[lane.ID] = &cp
	for _, record := range records {
		cp := record
		f.tix[record.ID] = &cp
		f.order = append(f.order, record.ID)
	}
	return nil
}

func (f *fakeDecomposeStore) GetRun(_ context.Context, runID string) (store.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[runID]
	if !ok {
		return store.Run{}, store.ErrNotFound
	}
	return r, nil
}

// ticketByID returns a copy of a ticket (for assertions).
func (f *fakeDecomposeStore) ticketByID(id string) (store.Ticket, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tix[id]
	if !ok {
		return store.Ticket{}, false
	}
	return *t, true
}

// ── Canned plan ───────────────────────────────────────────────────────────────

// cannedPlan returns a fixed set of tickets that covers all dependency shapes:
//
//	scaffold     → no deps
//	engine       → [scaffold]
//	feature1     → [scaffold, engine]
//	integration1 → [feature1]
//	polish       → [scaffold, engine, feature1, integration1]
func cannedPlan(_ context.Context, _ string, _ interface{}) ([]tickets.PlanTicket, error) {
	return []tickets.PlanTicket{
		{
			Key:      "scaffold",
			Title:    "Xcode scaffold",
			Detail:   "Create project.yml and app entry.",
			Priority: 3,
			Deps:     []string{},
		},
		{
			Key:      "engine",
			Title:    "On-device engine",
			Detail:   "Implement the core ML engine.",
			Priority: 3,
			Deps:     []string{"scaffold"},
		},
		{
			Key:      "feature1",
			Title:    "Feature: reminders",
			Detail:   "Add reminder UI, backed by SwiftData.",
			Priority: 2,
			Deps:     []string{"scaffold", "engine"},
		},
		{
			Key:      "integration1",
			Title:    "WidgetKit integration",
			Detail:   "Add a home-screen widget showing the next reminder.",
			Priority: 2,
			Deps:     []string{"feature1"},
		},
		{
			Key:      "polish",
			Title:    "Polish + verify",
			Detail:   "Fix warnings, run tests, ensure build succeeds.",
			Priority: 1,
			Deps:     []string{"scaffold", "engine", "feature1", "integration1"},
		},
	}, nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestDecomposeToTickets_LaneCreated(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{
		AppName: "TestApp",
		Concept: "smart reminders",
	}

	laneID, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("DecomposeToTickets: %v", err)
	}
	if laneID == "" {
		t.Fatal("expected non-empty laneID")
	}
	if len(ticketIDs) == 0 {
		t.Fatal("expected at least one ticketID")
	}

	// Lane must exist in the store.
	st.mu.Lock()
	lane, ok := st.lanes[laneID]
	st.mu.Unlock()
	if !ok {
		t.Fatalf("lane %q not found in store", laneID)
	}
	if lane.Name != "TestApp" {
		t.Errorf("lane name: got %q, want %q", lane.Name, "TestApp")
	}
}

func TestDecomposeToTickets_TicketCount(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "TestApp", Concept: "core"}

	_, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// cannedPlan returns 5 tickets.
	if len(ticketIDs) != 5 {
		t.Errorf("expected 5 tickets, got %d", len(ticketIDs))
	}
}

func TestDecomposeToTickets_DepsResolvedToIDs(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "TestApp", Concept: "core"}

	_, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All IDs in ticketIDs should exist in the store.
	known := make(map[string]bool, len(ticketIDs))
	for _, id := range ticketIDs {
		known[id] = true
		t, ok := st.ticketByID(id)
		if !ok {
			continue
		}
		// Every dep ID must itself be a known ticket ID.
		for _, depID := range t.Deps {
			if !known[depID] {
				// depID might have been inserted before this ticket — check the
				// full known set after the loop via the order slice.
				_ = depID
			}
		}
	}

	// Check no ticket has a dep that is NOT in the set of created ticket IDs.
	ticketIDSet := make(map[string]bool, len(ticketIDs))
	for _, id := range ticketIDs {
		ticketIDSet[id] = true
	}
	for _, id := range ticketIDs {
		tkt, ok := st.ticketByID(id)
		if !ok {
			t.Fatalf("ticket %q not found in store", id)
		}
		for _, depID := range tkt.Deps {
			if !ticketIDSet[depID] {
				t.Errorf("ticket %q has dep %q which is not in the created set — dangling dep", id, depID)
			}
		}
	}
}

func TestDecomposeToTickets_InsertionOrderRespectsDeps(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "TestApp", Concept: "core"}

	_, _, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Build position map: ticketID → insertion index.
	pos := make(map[string]int, len(st.order))
	for i, id := range st.order {
		pos[id] = i
	}

	// Every dep must have been inserted BEFORE the ticket that depends on it.
	for _, id := range st.order {
		tkt, _ := st.ticketByID(id)
		for _, depID := range tkt.Deps {
			if pos[depID] >= pos[id] {
				t.Errorf("dep insertion order violated: ticket %q (pos %d) has dep %q (pos %d) which was inserted after it",
					id, pos[id], depID, pos[depID])
			}
		}
	}
}

func TestDecomposeToTickets_NoDanglingDeps(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "TestApp", Concept: "core"}

	_, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	all := make(map[string]bool, len(ticketIDs))
	for _, id := range ticketIDs {
		all[id] = true
	}
	for _, id := range ticketIDs {
		tkt, _ := st.ticketByID(id)
		for _, dep := range tkt.Deps {
			if !all[dep] {
				t.Errorf("ticket %q: dep %q is not in the ticket set", id, dep)
			}
		}
	}
}

func TestDecomposeToTickets_MissingAppNameErrors(t *testing.T) {
	st := newFakeDecomposeStore()
	_, _, err := tickets.DecomposeToTickets(context.Background(), st, tickets.DecomposeConfig{}, cannedPlan)
	if err == nil {
		t.Fatal("expected error for missing AppName, got nil")
	}
}

func TestDecomposeToTickets_PlannerErrorPropagated(t *testing.T) {
	st := newFakeDecomposeStore()
	boom := func(_ context.Context, _ string, _ interface{}) ([]tickets.PlanTicket, error) {
		return nil, fmt.Errorf("boom")
	}
	cfg := tickets.DecomposeConfig{AppName: "X"}
	_, _, err := tickets.DecomposeToTickets(context.Background(), st, cfg, boom)
	if err == nil {
		t.Fatal("expected error from failing planner, got nil")
	}
}

func TestDecomposeToTickets_CycleDetected(t *testing.T) {
	st := newFakeDecomposeStore()
	cyclePlanner := func(_ context.Context, _ string, _ interface{}) ([]tickets.PlanTicket, error) {
		return []tickets.PlanTicket{
			{Key: "a", Title: "A", Detail: "a", Priority: 1, Deps: []string{"b"}},
			{Key: "b", Title: "B", Detail: "b", Priority: 1, Deps: []string{"a"}},
		}, nil
	}
	cfg := tickets.DecomposeConfig{AppName: "CycleApp"}
	_, _, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cyclePlanner)
	if err == nil {
		t.Fatal("expected cycle detection error, got nil")
	}
}

func TestDecomposeToTickets_UnknownDepErrors(t *testing.T) {
	st := newFakeDecomposeStore()
	badPlanner := func(_ context.Context, _ string, _ interface{}) ([]tickets.PlanTicket, error) {
		return []tickets.PlanTicket{
			{Key: "only", Title: "Only", Detail: "only", Priority: 1, Deps: []string{"ghost"}},
		}, nil
	}
	cfg := tickets.DecomposeConfig{AppName: "BadApp"}
	_, _, err := tickets.DecomposeToTickets(context.Background(), st, cfg, badPlanner)
	if err == nil {
		t.Fatal("expected error for unknown dep key, got nil")
	}
}

func TestDecomposeFromRunUsesCurrentSDKCheckpoint(t *testing.T) {
	st := newFakeDecomposeStore()
	st.runs["source"] = store.Run{RunID: "source", Project: "acme", Goal: "Data report", State: "passed", Options: json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","instruction":"Produce report","nodes":[{"id":"draft","title":"Draft","instruction":"Summarize sales"}]},"continuation":{"checkpoint":{"text":"FOREIGN ANCESTOR"}}}`), Doc: json.RawMessage(`{"sdk":{"phase":"passed","nodes":[{"id":"draft","phase":"passed","text":"CSV totals are model reported","findings":["Needs owner review"]}]},"legacy":"FOREIGN GRAPH"}`)}
	planner := func(ctx context.Context, prompt string, schema interface{}) ([]tickets.PlanTicket, error) {
		for _, want := range []string{"Summarize sales", "CSV totals", "Needs owner review", "not independent verification"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("missing %q", want)
			}
		}
		for _, unwanted := range []string{"FOREIGN", "Xcode", "SwiftData"} {
			if strings.Contains(prompt, unwanted) {
				t.Errorf("unexpected %q", unwanted)
			}
		}
		return cannedPlan(ctx, prompt, schema)
	}
	laneID, ids, err := tickets.DecomposeFromRun(t.Context(), st, "source", "/target", planner)
	if err != nil {
		t.Fatal(err)
	}
	lane := st.lanes[laneID]
	if lane.Name != "Data report" || lane.Project != "acme" || lane.OriginRun != "source" || len(ids) == 0 {
		t.Fatalf("unexpected lane: %+v", lane)
	}
}

func TestDecomposeFromRunRejectsInvalidSourcesBeforePlanning(t *testing.T) {
	valid := store.Run{RunID: "source", State: "passed", Options: json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"checked-task","instruction":"Report"}}`), Doc: json.RawMessage(`{"sdk":{"phase":"passed","text":"Done"}}`)}
	cases := []struct {
		name   string
		mutate func(*store.Run)
	}{
		{"missing", func(r *store.Run) { r.RunID = "other" }},
		{"graph", func(r *store.Run) { r.Options = json.RawMessage(`{"spec":{}}`) }},
		{"running", func(r *store.Run) { r.State = "running" }},
		{"rehearsal", func(r *store.Run) { r.Mode = "dry-run" }},
		{"malformed", func(r *store.Run) { r.Doc = json.RawMessage(`{"sdk":`) }},
		{"missing checkpoint", func(r *store.Run) { r.Doc = json.RawMessage(`{}`) }},
		{"unsupported", func(r *store.Run) {
			r.Options = json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"unknown"}}`)
		}},
		{"mismatch", func(r *store.Run) {
			r.Options = json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"a"}]}}`)
			r.Doc = json.RawMessage(`{"sdk":{"phase":"passed","nodes":[{"id":"b","phase":"passed"}]}}`)
		}},
		{"duplicate", func(r *store.Run) {
			r.Options = json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"node-workflow","nodes":[{"id":"a"},{"id":"a"}]}}`)
			r.Doc = json.RawMessage(`{"sdk":{"phase":"passed","nodes":[{"id":"a","phase":"passed"},{"id":"a","phase":"passed"}]}}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeDecomposeStore()
			run := valid
			tc.mutate(&run)
			st.runs[run.RunID] = run
			planner := func(context.Context, string, interface{}) ([]tickets.PlanTicket, error) {
				t.Fatal("planner called")
				return nil, nil
			}
			if _, _, err := tickets.DecomposeFromRun(t.Context(), st, "source", "/target", planner); err == nil {
				t.Fatal("invalid source accepted")
			}
			if len(st.lanes) != 0 || len(st.tix) != 0 {
				t.Fatal("invalid source created work")
			}
		})
	}
}

// TestDecomposeToTickets_TicketsAllInCorrectLane confirms every ticket has the
// right laneID set.
func TestDecomposeToTickets_TicketsAllInCorrectLane(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "LaneCheck", Concept: "test"}

	laneID, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range ticketIDs {
		tkt, ok := st.ticketByID(id)
		if !ok {
			t.Fatalf("ticket %q not in store", id)
		}
		if tkt.LaneID != laneID {
			t.Errorf("ticket %q: laneID=%q, want %q", id, tkt.LaneID, laneID)
		}
		if tkt.Status != "todo" {
			t.Errorf("ticket %q: status=%q, want todo", id, tkt.Status)
		}
	}
}

// TestDecomposeToTickets_PriorityBounds confirms priorities are clamped to [1,3].
func TestDecomposeToTickets_PriorityBounds(t *testing.T) {
	badPriorities := func(_ context.Context, _ string, _ interface{}) ([]tickets.PlanTicket, error) {
		return []tickets.PlanTicket{
			{Key: "low", Title: "Low", Detail: "low", Priority: 0, Deps: []string{}},
			{Key: "high", Title: "High", Detail: "high", Priority: 99, Deps: []string{}},
		}, nil
	}
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "PrioApp"}
	_, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, badPriorities)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range ticketIDs {
		tkt, _ := st.ticketByID(id)
		if tkt.Priority < 1 || tkt.Priority > 3 {
			t.Errorf("ticket %q: priority %d out of [1,3]", id, tkt.Priority)
		}
	}
}

// TestBuildPlanPrompt confirms the prompt contains the required sections.
func TestBuildPlanPrompt(t *testing.T) {
	cfg := tickets.DecomposeConfig{
		AppName:            "PromptApp",
		Concept:            "smart notifications",
		Features:           []string{"feed", "alerts"},
		NativeIntegrations: []string{"WidgetKit", "App Intents"},
	}
	prompt := tickets.BuildPlanPrompt(cfg)

	for _, want := range []string{
		"PromptApp",
		"smart notifications",
		"feed",
		"alerts",
		"WidgetKit",
		"App Intents",
		"acceptance criteria",
		"JSON array",
	} {
		if !containsStr(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestDecomposeToTickets_IdsUnique verifies all returned IDs are distinct.
func TestDecomposeToTickets_IdsUnique(t *testing.T) {
	st := newFakeDecomposeStore()
	cfg := tickets.DecomposeConfig{AppName: "UniqueApp", Concept: "test"}
	_, ticketIDs, err := tickets.DecomposeToTickets(context.Background(), st, cfg, cannedPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seen := make(map[string]bool, len(ticketIDs))
	for _, id := range ticketIDs {
		if seen[id] {
			t.Errorf("duplicate ticket ID: %q", id)
		}
		seen[id] = true
	}
}

// Ensure the fake compiles even if time is not used directly in this file.

func TestDecomposePreservesGenericModelInstructions(t *testing.T) {
	st := newFakeDecomposeStore()
	detail := "Use SalesModel from the existing Python module. Acceptance: reconcile all totals."
	planner := func(context.Context, string, interface{}) ([]tickets.PlanTicket, error) {
		return []tickets.PlanTicket{{Key: "first", Title: "Calculate", Detail: detail, Priority: 3}, {Key: "second", Title: "Report", Detail: detail, Priority: 2, Deps: []string{"first"}}}, nil
	}
	_, ids, err := tickets.DecomposeToTickets(t.Context(), st, tickets.DecomposeConfig{AppName: "Data report"}, planner)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if st.tix[id].Detail != detail {
			t.Fatalf("planner instructions rewritten: %s", st.tix[id].Detail)
		}
	}
}

func TestDecomposeFromRunBoundsEachEvidenceField(t *testing.T) {
	st := newFakeDecomposeStore()
	st.runs["source"] = store.Run{RunID: "source", State: "passed", Options: json.RawMessage(`{"runtime":"codex-sdk","request":{"workflow":"checked-task"}}`)}
	run := st.runs["source"]
	run.Doc, _ = json.Marshal(map[string]any{"sdk": map[string]any{"phase": "passed", "text": strings.Repeat("x", 50000), "findings": []string{"Reserved finding"}}, "files": strings.Repeat("unused", 10000)})
	st.runs["source"] = run
	cfg, err := tickets.ConfigFromRun(t.Context(), st, "source", "/target")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Evidence) > 16000 || !strings.Contains(cfg.Evidence, "Reserved finding") || strings.Contains(cfg.Evidence, "unused") {
		t.Fatalf("unbounded or unrelated evidence: len=%d", len(cfg.Evidence))
	}
}

func TestDecomposeSourceErrorsDistinguishMissingFromInvalid(t *testing.T) {
	st := newFakeDecomposeStore()
	if _, err := tickets.ConfigFromRun(t.Context(), st, "missing", ""); !errors.Is(err, store.ErrNotFound) || errors.Is(err, tickets.ErrInvalidSource) {
		t.Fatalf("missing run classification: %v", err)
	}
	st.runs["invalid"] = store.Run{RunID: "invalid", State: "passed"}
	if _, err := tickets.ConfigFromRun(t.Context(), st, "invalid", ""); !errors.Is(err, tickets.ErrInvalidSource) {
		t.Fatalf("invalid evidence classification: %v", err)
	}
}
