package store

import (
	"context"
	"errors"
	"testing"
)

func TestLanePlanAtomicPublication(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "atomic-plan-" + uniq()
	lane := &Lane{ID: id, Name: "Report", Status: "active", Project: id, OriginRun: "sdk-source", TargetRoot: "/target"}
	tickets := []Ticket{{ID: id + "-a", LaneID: id, Title: "Prepare", Detail: "Prepare inputs", Status: "todo", Priority: 3}, {ID: id + "-b", LaneID: id, Title: "Report", Detail: "Generate and review report", Status: "todo", Priority: 2, Deps: []string{id + "-a"}}}
	if err := st.CreateLaneWithTickets(ctx, lane, tickets); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetLane(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != id || got.OriginRun != "sdk-source" || got.TargetRoot != "/target" {
		t.Fatalf("lost lane context: %+v", got)
	}
	rows, err := st.ListTicketsByLane(ctx, id)
	if err != nil || len(rows) != 2 {
		t.Fatalf("tickets=%v err=%v", rows, err)
	}
	second, err := st.GetTicket(ctx, id+"-b")
	if err != nil || len(second.Deps) != 1 || second.Deps[0] != id+"-a" {
		t.Fatalf("lost deps: %+v %v", second, err)
	}
	if _, err := st.GetProject(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestLanePlanRollsBackAllWritesWhenLaterTicketFails(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	id := "rollback-plan-" + uniq()
	original := &Lane{ID: id + "-existing", Name: "Original", Status: "active"}
	if err := st.CreateLane(ctx, original); err != nil {
		t.Fatal(err)
	}
	conflict := Ticket{ID: id + "-conflict", LaneID: original.ID, Title: "Existing", Status: "todo", Priority: 1}
	if err := st.CreateTicket(ctx, &conflict); err != nil {
		t.Fatal(err)
	}
	lane := &Lane{ID: id, Name: "Rejected", Status: "active", Project: id}
	records := []Ticket{{ID: id + "-first", LaneID: id, Title: "First", Status: "todo", Priority: 1}, {ID: conflict.ID, LaneID: id, Title: "Conflict", Status: "todo", Priority: 1}}
	if err := st.CreateLaneWithTickets(ctx, lane, records); err == nil {
		t.Fatal("expected DB conflict")
	}
	if _, err := st.GetLane(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial lane: %v", err)
	}
	if _, err := st.GetTicket(ctx, id+"-first"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial ticket: %v", err)
	}
	if _, err := st.GetProject(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial project: %v", err)
	}
	got, err := st.GetTicket(ctx, conflict.ID)
	if err != nil || got.LaneID != original.ID {
		t.Fatalf("modified existing ticket: %+v %v", got, err)
	}
}

func TestLanePlanRejectsForeignDependenciesAndCancellation(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id := "invalid-plan-" + uniq()
	lane := &Lane{ID: id, Name: "Invalid", Status: "active"}
	for _, records := range [][]Ticket{
		{{ID: id + "-a", LaneID: id, Deps: []string{"foreign"}}},
		{{ID: id + "-a", LaneID: "foreign"}},
		{{ID: id + "-a", LaneID: id}, {ID: id + "-a", LaneID: id}},
	} {
		if err := st.CreateLaneWithTickets(t.Context(), lane, records); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := st.CreateLaneWithTickets(canceled, lane, []Ticket{{ID: id + "-a", LaneID: id}}); err == nil {
		t.Fatal("canceled insert accepted")
	}
	if _, err := st.GetLane(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid lane persisted: %v", err)
	}
}
