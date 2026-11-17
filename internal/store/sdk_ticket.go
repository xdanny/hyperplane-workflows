package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrTicketNotReady = errors.New("no ready task, or lane/task changed; refresh the Board")

// SDKTicketAdmission pins the saved task read for admission, without claiming it.
// The owner transaction rechecks this snapshot after workspace admission.
type SDKTicketAdmission struct {
	Lane   Lane
	Ticket Ticket
}

const readyTicket = `status='todo' AND NOT EXISTS (
 SELECT 1 FROM jsonb_array_elements_text(tickets.deps) dep
 WHERE NOT EXISTS (SELECT 1 FROM tickets d WHERE d.id=dep AND d.lane_id=tickets.lane_id AND d.status='done'))`

func (s *Store) NextReadySDKTicket(ctx context.Context, laneID string) (SDKTicketAdmission, error) {
	var out SDKTicketAdmission
	lane, err := s.GetLane(ctx, laneID)
	if err != nil {
		return out, err
	}
	if lane.Status != "active" {
		return out, ErrTicketNotReady
	}
	ticket, err := scanTicket(s.Pool.QueryRow(ctx, `SELECT `+ticketCols+` FROM tickets WHERE lane_id=$1 AND `+readyTicket+` ORDER BY priority DESC,created_at,id LIMIT 1`, laneID))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrTicketNotReady
	}
	if err != nil {
		return out, err
	}
	return SDKTicketAdmission{Lane: *lane, Ticket: *ticket}, nil
}

// StartTicket publishes the run, its initial checkpoint and its ticket claim in
// one short transaction on the SDK ownership connection. No model call occurs.
func (o *SDKRunOwner) StartTicket(ctx context.Context, source SDKTicketAdmission, run Run, checkpoint JSON) (Event, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.Context.Err(); err != nil {
		return Event{}, err
	}
	if run.RunID != o.runID || source.Ticket.ID == "" || source.Ticket.LaneID != source.Lane.ID {
		return Event{}, ErrTicketNotReady
	}
	tx, err := o.conn.Begin(ctx)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(ctx)
	lane, err := scanLane(tx.QueryRow(ctx, `SELECT `+laneCols+` FROM lanes WHERE id=$1 FOR UPDATE`, source.Lane.ID))
	if err != nil {
		return Event{}, err
	}
	if lane.Status != "active" || lane.Project != source.Lane.Project || lane.TargetRoot != source.Lane.TargetRoot || run.Project != lane.Project {
		return Event{}, ErrTicketNotReady
	}
	// A canceled run may still hold its session while the worker stops. Do not
	// admit another task in that lane until the actual owner has released it.
	rows, err := tx.Query(ctx, `SELECT sdk_run_id,status FROM tickets WHERE lane_id=$1 AND (status='in_progress' OR sdk_run_id<>'')`, lane.ID)
	if err != nil {
		return Event{}, err
	}
	var priorIDs []string
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			rows.Close()
			return Event{}, err
		}
		if status == "in_progress" {
			rows.Close()
			return Event{}, ErrTicketNotReady
		}
		if id != "" {
			priorIDs = append(priorIDs, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Event{}, err
	}
	for _, id := range priorIDs {
		var available bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,684795135))`, id).Scan(&available); err != nil {
			return Event{}, err
		}
		if !available {
			return Event{}, ErrTicketNotReady
		}
	}
	current, err := scanTicket(tx.QueryRow(ctx, `SELECT `+ticketCols+` FROM tickets WHERE id=$1 AND lane_id=$2 AND `+readyTicket+` FOR UPDATE`, source.Ticket.ID, lane.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrTicketNotReady
	}
	if err != nil {
		return Event{}, err
	}
	if !current.UpdatedAt.Equal(source.Ticket.UpdatedAt) || current.Title != source.Ticket.Title || current.Detail != source.Ticket.Detail {
		return Event{}, ErrTicketNotReady
	}
	var options struct {
		Runtime      string `json:"runtime"`
		Ownership    string `json:"ownership"`
		OwnerSession uint32 `json:"ownerSession"`
		TicketID     string `json:"ticketId"`
		Request      struct {
			Workflow string `json:"workflow"`
		} `json:"request"`
	}
	if json.Unmarshal(run.Options, &options) != nil || options.Runtime != "codex-sdk" || options.Ownership != SDKOwnership || options.OwnerSession != o.SessionID() || options.TicketID != current.ID || options.Request.Workflow != "checked-task" || run.State != "running" {
		return Event{}, ErrTicketNotReady
	}
	if lane.Project != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO projects(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, lane.Project, defaultProjectName(lane.Project)); err != nil {
			return Event{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO runs(run_id,dag,goal,state,mode,type,started_at,owner_pid,options,project) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, run.RunID, run.DAG, run.Goal, run.State, run.Mode, run.Type, timePtr(run.StartedAt), run.OwnerPID, jsonOr(run.Options), run.Project)
	if err != nil {
		return Event{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tickets SET status='in_progress',sdk_run_id=$2,assignee='SDK',output='',error='',review_verdict='',attempts=attempts+1,updated_at=now() WHERE id=$1`, current.ID, run.RunID); err != nil {
		return Event{}, err
	}
	_, event, err := saveSDKCheckpointTx(ctx, tx, run.RunID, run.Project, run.OwnerPID, checkpoint, "", nil)
	if err != nil {
		return Event{}, err
	}
	if err := o.Context.Err(); err != nil {
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, fmt.Errorf("ticket admission commit uncertain; refresh Board before retrying: %w", err)
	}
	return event, nil
}

// Saved terminal SDK state and its linked Board state move together. An older
// attempt cannot overwrite a newer link or a person's later decision.
func finishSDKTicket(ctx context.Context, tx pgx.Tx, runID, state string, checkpoint JSON) error {
	var result struct {
		Text     string   `json:"text"`
		Error    string   `json:"error"`
		Findings []string `json:"findings"`
	}
	if err := json.Unmarshal(checkpoint, &result); err != nil {
		return err
	}
	status := "blocked"
	if state == "passed" {
		status = "review"
	}
	reason := result.Error
	if reason == "" && state != "passed" {
		reason = strings.Join(result.Findings, "; ")
	}
	_, err := tx.Exec(ctx, `UPDATE tickets SET status=$2,output=$3,error=$4,updated_at=now() WHERE sdk_run_id=$1 AND status='in_progress'`, runID, status, result.Text, reason)
	return err
}
