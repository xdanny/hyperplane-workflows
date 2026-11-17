package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const laneCols = `id, name, description, status, target_root, project, origin_run, created_at`
const ticketCols = `id, lane_id, title, detail, status, priority, assignee, deps, output, error, attempts, review_verdict, created_at, updated_at, sdk_run_id`

// CreateLane inserts a new lane.
func (s *Store) CreateLane(ctx context.Context, l *Lane) error {
	// Auto-register the lane's project tag (no-op for an unscoped lane).
	if err := s.EnsureProject(ctx, l.Project); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO lanes (id, name, description, status, target_root, project, origin_run, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`,
		l.ID, l.Name, l.Description, l.Status, l.TargetRoot, l.Project, l.OriginRun)
	if err != nil {
		return fmt.Errorf("create lane: %w", err)
	}
	return nil
}

func scanLane(row pgx.Row) (*Lane, error) {
	var l Lane
	if err := row.Scan(&l.ID, &l.Name, &l.Description, &l.Status, &l.TargetRoot, &l.Project, &l.OriginRun, &l.CreatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

// GetLane returns a lane by ID.
func (s *Store) GetLane(ctx context.Context, id string) (*Lane, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+laneCols+` FROM lanes WHERE id=$1`, id)
	l, err := scanLane(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get lane: %w", err)
	}
	return l, nil
}

// ListLanes returns lanes with their ticket status counts, newest first.
// project distinguishes "no filter" from "filter to a specific value" (which
// may itself be empty, meaning unscoped-only): nil returns all lanes; a
// non-nil pointer returns only lanes whose project equals *project — so
// StrPtr("") returns only unscoped lanes, not everything.
func (s *Store) ListLanes(ctx context.Context, project *string) ([]*LaneWithCounts, error) {
	q := `
		SELECT
			l.id, l.name, l.description, l.status, l.target_root, l.project, l.origin_run, l.created_at,
			COUNT(t.id) FILTER (WHERE t.status='todo')        AS todo,
			COUNT(t.id) FILTER (WHERE t.status='in_progress') AS in_progress,
			COUNT(t.id) FILTER (WHERE t.status='review')      AS review,
			COUNT(t.id) FILTER (WHERE t.status='done')        AS done,
			COUNT(t.id) FILTER (WHERE t.status='blocked')     AS blocked
		FROM lanes l
		LEFT JOIN tickets t ON t.lane_id = l.id`
	var args []any
	if project != nil {
		args = append(args, *project)
		q += ` WHERE l.project = $1`
	}
	q += `
		GROUP BY l.id, l.name, l.description, l.status, l.target_root, l.project, l.origin_run, l.created_at
		ORDER BY l.created_at DESC`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list lanes: %w", err)
	}
	defer rows.Close()
	var out []*LaneWithCounts
	for rows.Next() {
		var lc LaneWithCounts
		if err := rows.Scan(
			&lc.ID, &lc.Name, &lc.Description, &lc.Status, &lc.TargetRoot, &lc.Project, &lc.OriginRun, &lc.CreatedAt,
			&lc.Counts.Todo, &lc.Counts.InProgress, &lc.Counts.Review,
			&lc.Counts.Done, &lc.Counts.Blocked,
		); err != nil {
			return nil, fmt.Errorf("scan lane: %w", err)
		}
		out = append(out, &lc)
	}
	return out, rows.Err()
}

// scanTicket reads a ticket row. deps is stored as JSONB []string.
func scanTicket(row pgx.Row) (*Ticket, error) {
	var t Ticket
	var depsRaw []byte
	if err := row.Scan(
		&t.ID, &t.LaneID, &t.Title, &t.Detail, &t.Status,
		&t.Priority, &t.Assignee, &depsRaw, &t.Output, &t.Error,
		&t.Attempts, &t.ReviewVerdict, &t.CreatedAt, &t.UpdatedAt, &t.SDKRunID,
	); err != nil {
		return nil, err
	}
	if len(depsRaw) > 0 {
		if err := json.Unmarshal(depsRaw, &t.Deps); err != nil {
			return nil, fmt.Errorf("unmarshal deps: %w", err)
		}
	}
	if t.Deps == nil {
		t.Deps = []string{}
	}
	return &t, nil
}

// CreateTicket inserts a new ticket into a lane.
func (s *Store) CreateTicket(ctx context.Context, t *Ticket) error {
	deps := t.Deps
	if deps == nil {
		deps = []string{}
	}
	depsJSON, err := json.Marshal(deps)
	if err != nil {
		return fmt.Errorf("marshal deps: %w", err)
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO tickets (id, lane_id, title, detail, status, priority, assignee, deps, output, error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW(),NOW())`,
		t.ID, t.LaneID, t.Title, t.Detail, t.Status, t.Priority, t.Assignee,
		depsJSON, t.Output, t.Error)
	if err != nil {
		return fmt.Errorf("create ticket: %w", err)
	}
	return nil
}

// GetTicket returns a ticket by ID.
func (s *Store) GetTicket(ctx context.Context, id string) (*Ticket, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+ticketCols+` FROM tickets WHERE id=$1`, id)
	t, err := scanTicket(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get ticket: %w", err)
	}
	return t, nil
}

// ListTicketsByLane returns all tickets in a lane, ordered by priority desc then created_at asc.
func (s *Store) ListTicketsByLane(ctx context.Context, laneID string) ([]*Ticket, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+ticketCols+`
		FROM tickets
		WHERE lane_id=$1
		ORDER BY priority DESC, created_at ASC`, laneID)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ticket: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

var ErrTicketChanged = errors.New("task changed while editing; refresh the Board and try again")

// UpdateTicketStatus records a person's Board edit only after the SDK owner
// releases a linked attempt. SDK checkpoint writes use their own transaction.
func (s *Store) UpdateTicketStatus(ctx context.Context, id, status, assignee, output, errText string, expectedUpdatedAt time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var runID string
	var updatedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT sdk_run_id, updated_at FROM tickets WHERE id=$1 FOR UPDATE`, id).Scan(&runID, &updatedAt); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !updatedAt.Equal(expectedUpdatedAt) {
		return ErrTicketChanged
	}
	if runID != "" {
		var released bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,684795135))`, runID).Scan(&released); err != nil {
			return err
		}
		if !released {
			return ErrRunActive
		}
		var running bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=$1 AND state='running')`, runID).Scan(&running); err != nil {
			return err
		}
		if running {
			return ErrRunActive
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE tickets SET status=$2,assignee=$3,output=$4,error=$5,updated_at=now() WHERE id=$1`, id, status, assignee, output, errText); err != nil {
		return fmt.Errorf("update ticket status: %w", err)
	}
	return tx.Commit(ctx)
}
