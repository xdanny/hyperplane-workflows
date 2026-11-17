package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func sdkTicketSource(t *testing.T, st *Store) SDKTicketAdmission {
	t.Helper()
	id := "sdk-board-" + uniq()
	lane := &Lane{ID: id, Name: "Work", Status: "active", TargetRoot: "/target", Project: id}
	if err := st.CreateLaneWithTickets(t.Context(), lane, []Ticket{{ID: id + "-task", LaneID: id, Title: "Task", Detail: "Do work", Status: "todo", Priority: 1}}); err != nil {
		t.Fatal(err)
	}
	source, err := st.NextReadySDKTicket(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return source
}
func sdkTicketRun(t *testing.T, source SDKTicketAdmission, owner *SDKRunOwner) Run {
	t.Helper()
	return Run{RunID: owner.runID, DAG: "checked-task", Goal: source.Ticket.Title, Project: source.Lane.Project, State: "running", Mode: "run", Type: "sdk-task", StartedAt: time.Now(), OwnerPID: os.Getpid(), Options: JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"%s","ownerSession":%d,"ticketId":%q,"request":{"workflow":"checked-task"}}`, SDKOwnership, owner.SessionID(), source.Ticket.ID))}
}
func TestSDKTicketAdmissionRollsBackFailedLink(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source := sdkTicketSource(t, st)
	id := source.Lane.ID + "-run"
	owner, err := st.OwnSDKRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := st.Pool.Exec(t.Context(), `ALTER TABLE tickets ADD CONSTRAINT test_board_link CHECK(sdk_run_id='') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(t.Context(), `ALTER TABLE tickets DROP CONSTRAINT test_board_link`)
	if _, err := owner.StartTicket(t.Context(), source, sdkTicketRun(t, source, owner), JSON(`{"phase":"starting"}`)); err == nil {
		t.Fatal("failed link accepted")
	}
	ticket, err := st.GetTicket(t.Context(), source.Ticket.ID)
	if err != nil || ticket.Status != "todo" || ticket.SDKRunID != "" {
		t.Fatalf("partial claim %+v %v", ticket, err)
	}
	if _, err := st.GetRun(t.Context(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial run: %v", err)
	}
}
func TestSDKTicketTerminalAtomicityAndCrashRecovery(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source := sdkTicketSource(t, st)
	id := source.Lane.ID + "-run"
	owner, err := st.OwnSDKRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	run := sdkTicketRun(t, source, owner)
	if _, err := owner.StartTicket(t.Context(), source, run, JSON(`{"phase":"starting"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(t.Context(), `ALTER TABLE tickets ADD CONSTRAINT test_board_terminal CHECK(status<>'review') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	_, _, err = owner.SaveCheckpoint(t.Context(), id, run.Project, run.OwnerPID, JSON(`{"phase":"passed","text":"Completed"}`), "passed", nil)
	if err == nil {
		t.Fatal("terminal ticket failure swallowed")
	}
	if _, err := st.Pool.Exec(t.Context(), `ALTER TABLE tickets DROP CONSTRAINT test_board_terminal`); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetRun(t.Context(), id)
	if err != nil || current.State != "running" {
		t.Fatalf("partially terminal run %+v %v", current, err)
	}
	ticket, err := st.GetTicket(t.Context(), source.Ticket.ID)
	if err != nil || ticket.Status != "in_progress" {
		t.Fatalf("partially terminal ticket %+v %v", ticket, err)
	}
	events, err := st.ListEvents(t.Context(), id, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("partial event %v %v", events, err)
	}
	// An abandoned SDK owner is interrupted, never automatically retried.
	owner.Close()
	if _, err := st.ReconcileOrphans(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, _ = st.GetRun(t.Context(), id)
	ticket, _ = st.GetTicket(t.Context(), source.Ticket.ID)
	if current.State != "interrupted" || ticket.Status != "blocked" || ticket.SDKRunID != id {
		t.Fatalf("recovery not linked: %s %+v", current.State, ticket)
	}
	if err := st.UpdateTicketStatus(t.Context(), ticket.ID, "todo", "", ticket.Output, ticket.Error, ticket.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	retry, err := st.NextReadySDKTicket(t.Context(), source.Lane.ID)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := st.OwnSDKRun(t.Context(), id+"-retry")
	if err != nil {
		t.Fatal(err)
	}
	defer successor.Close()
	if _, err := successor.StartTicket(t.Context(), retry, sdkTicketRun(t, retry, successor), JSON(`{"phase":"starting"}`)); err != nil {
		t.Fatal(err)
	}
	// Even explicitly supplied old terminal evidence cannot overwrite the new link.
	if _, _, err := st.SaveSDKCheckpoint(t.Context(), id, run.Project, run.OwnerPID, JSON(`{"phase":"passed","text":"STALE"}`), "passed", nil); err == nil {
		t.Fatal("stale owner checkpoint accepted")
	}
	ticket, _ = st.GetTicket(t.Context(), ticket.ID)
	if ticket.SDKRunID != successor.runID || ticket.Status != "in_progress" {
		t.Fatalf("stale overwrite %+v", ticket)
	}
}
func TestSDKTicketRejectsChangedSourceAndDanglingDependency(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	source := sdkTicketSource(t, st)
	owner, err := st.OwnSDKRun(t.Context(), source.Lane.ID+"-run")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := st.UpdateTicketStatus(t.Context(), source.Ticket.ID, "todo", "human", "", "", source.Ticket.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.StartTicket(t.Context(), source, sdkTicketRun(t, source, owner), JSON(`{"phase":"starting"}`)); !errors.Is(err, ErrTicketNotReady) {
		t.Fatalf("changed source admitted: %v", err)
	}
	raw, _ := json.Marshal([]string{"missing-dependency"})
	if _, err := st.Pool.Exec(t.Context(), `UPDATE tickets SET deps=$2 WHERE id=$1`, source.Ticket.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.NextReadySDKTicket(t.Context(), source.Lane.ID); !errors.Is(err, ErrTicketNotReady) {
		t.Fatalf("dangling dependency ready: %v", err)
	}
}
