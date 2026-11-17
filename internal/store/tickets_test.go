package store

import (
	"context"
	"testing"
)

func TestLaneCRUD(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "lane-test-" + uniq()
	lane := &Lane{
		ID:          id,
		Name:        "Test Lane",
		Description: "A test lane",
		Status:      "active",
		TargetRoot:  "/tmp/test",
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM lanes WHERE id=$1`, id) })

	if err := st.CreateLane(ctx, lane); err != nil {
		t.Fatalf("create lane: %v", err)
	}

	got, err := st.GetLane(ctx, id)
	if err != nil {
		t.Fatalf("get lane: %v", err)
	}
	if got.Name != "Test Lane" || got.Status != "active" || got.TargetRoot != "/tmp/test" {
		t.Fatalf("unexpected lane: %+v", got)
	}

	lanes, err := st.ListLanes(ctx, nil)
	if err != nil {
		t.Fatalf("list lanes: %v", err)
	}
	var found bool
	for _, l := range lanes {
		if l.ID == id {
			found = true
			// Counts should all be zero since no tickets added yet.
			if l.Counts.Todo != 0 || l.Counts.InProgress != 0 {
				t.Fatalf("expected zero counts, got %+v", l.Counts)
			}
		}
	}
	if !found {
		t.Fatalf("lane not found in list")
	}
}

func TestTicketCRUD(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	laneID := "lane-tkt-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM lanes WHERE id=$1`, laneID) })

	if err := st.CreateLane(ctx, &Lane{ID: laneID, Name: "ticket test lane", Status: "active"}); err != nil {
		t.Fatalf("create lane: %v", err)
	}

	tkID := "tkt-" + uniq()
	tk := &Ticket{
		ID:       tkID,
		LaneID:   laneID,
		Title:    "Build login screen",
		Detail:   "Use SwiftUI",
		Status:   "todo",
		Priority: 10,
		Deps:     []string{},
	}

	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	got, err := st.GetTicket(ctx, tkID)
	if err != nil {
		t.Fatalf("get ticket: %v", err)
	}
	if got.Title != "Build login screen" || got.Priority != 10 || got.Status != "todo" {
		t.Fatalf("unexpected ticket: %+v", got)
	}
	if len(got.Deps) != 0 {
		t.Fatalf("expected empty deps, got %v", got.Deps)
	}

	// Update status.
	if err := st.UpdateTicketStatus(ctx, tkID, "review", "worker-0", "output here", "", got.UpdatedAt); err != nil {
		t.Fatalf("update ticket status: %v", err)
	}

	updated, err := st.GetTicket(ctx, tkID)
	if err != nil {
		t.Fatalf("get ticket after update: %v", err)
	}
	if updated.Status != "review" || updated.Assignee != "worker-0" || updated.Output != "output here" {
		t.Fatalf("unexpected updated ticket: %+v", updated)
	}

	// List by lane.
	tix, err := st.ListTicketsByLane(ctx, laneID)
	if err != nil {
		t.Fatalf("list tickets: %v", err)
	}
	if len(tix) != 1 || tix[0].ID != tkID {
		t.Fatalf("unexpected list: %v", tix)
	}

	// Counts in ListLanes should reflect the status.
	lanes, err := st.ListLanes(ctx, nil)
	if err != nil {
		t.Fatalf("list lanes: %v", err)
	}
	for _, l := range lanes {
		if l.ID == laneID {
			if l.Counts.Review != 1 || l.Counts.Todo != 0 {
				t.Fatalf("expected Counts.Review=1, got %+v", l.Counts)
			}
		}
	}
}

// TestLaneProjectAndOriginRun verifies project + origin_run round-trip through
// CreateLane/GetLane, and that ListLanes distinguishes "no filter" (nil —
// returns everything) from "filter to a specific value" (non-nil, including
// a pointer to "" for unscoped-only).
func TestLaneProjectAndOriginRun(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	scopedID := "lane-proj-" + uniq()
	unscopedID := "lane-unscoped-" + uniq()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM lanes WHERE id=$1 OR id=$2`, scopedID, unscopedID)
	})

	scoped := &Lane{
		ID:        scopedID,
		Name:      "Scoped Lane",
		Status:    "active",
		Project:   "acme",
		OriginRun: "run-origin-123",
	}
	if err := st.CreateLane(ctx, scoped); err != nil {
		t.Fatalf("create scoped lane: %v", err)
	}
	unscoped := &Lane{ID: unscopedID, Name: "Unscoped Lane", Status: "active"}
	if err := st.CreateLane(ctx, unscoped); err != nil {
		t.Fatalf("create unscoped lane: %v", err)
	}

	got, err := st.GetLane(ctx, scopedID)
	if err != nil {
		t.Fatalf("get lane: %v", err)
	}
	if got.Project != "acme" || got.OriginRun != "run-origin-123" {
		t.Fatalf("unexpected project/originRun: %+v", got)
	}

	gotUnscoped, err := st.GetLane(ctx, unscopedID)
	if err != nil {
		t.Fatalf("get unscoped lane: %v", err)
	}
	if gotUnscoped.Project != "" || gotUnscoped.OriginRun != "" {
		t.Fatalf("expected empty project/originRun by default, got %+v", gotUnscoped)
	}

	// ListLanes(nil) returns everything — no filter applied.
	all, err := st.ListLanes(ctx, nil)
	if err != nil {
		t.Fatalf("list all lanes: %v", err)
	}
	var sawScoped, sawUnscoped bool
	for _, l := range all {
		if l.ID == scopedID {
			sawScoped = true
		}
		if l.ID == unscopedID {
			sawUnscoped = true
		}
	}
	if !sawScoped || !sawUnscoped {
		t.Fatalf("expected both lanes in unfiltered list: scoped=%v unscoped=%v", sawScoped, sawUnscoped)
	}

	// ListLanes(StrPtr("acme")) returns only the scoped lane.
	scopedList, err := st.ListLanes(ctx, StrPtr("acme"))
	if err != nil {
		t.Fatalf("list scoped lanes: %v", err)
	}
	if len(scopedList) != 1 || scopedList[0].ID != scopedID {
		t.Fatalf("expected only scoped lane, got %+v", scopedList)
	}

	// ListLanes(StrPtr("")) — the "unscoped" chip — returns only lanes with no
	// project, never the whole board. This is the case the shared
	// ProjectSelector component must be able to express: distinct from "all".
	unscopedList, err := st.ListLanes(ctx, StrPtr(""))
	if err != nil {
		t.Fatalf("list unscoped lanes: %v", err)
	}
	var sawScopedInUnscoped bool
	var sawUnscopedInUnscoped bool
	for _, l := range unscopedList {
		if l.ID == scopedID {
			sawScopedInUnscoped = true
		}
		if l.ID == unscopedID {
			sawUnscopedInUnscoped = true
		}
	}
	if sawScopedInUnscoped {
		t.Fatalf("ListLanes(StrPtr(\"\")) must not include project-scoped lanes, got %+v", unscopedList)
	}
	if !sawUnscopedInUnscoped {
		t.Fatalf("ListLanes(StrPtr(\"\")) must include the unscoped lane, got %+v", unscopedList)
	}
}
