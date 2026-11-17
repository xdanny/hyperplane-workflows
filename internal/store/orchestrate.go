package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrchestrateRunRecord is the DB record for a backlog orchestration loop invocation.
type OrchestrateRunRecord struct {
	ID          string          `json:"id"`
	StartedAt   time.Time       `json:"startedAt"`
	BacklogSize int             `json:"backlogSize"`
	MaxItems    int             `json:"maxItems"`
	Done        int             `json:"done"`
	Failed      int             `json:"failed"`
	Items       json.RawMessage `json:"items"`
	Status      string          `json:"status"`
}

// CreateOrchestrateRunParams are the required fields to open a new orchestrate run.
type CreateOrchestrateRunParams struct {
	ID          string
	BacklogSize int
	MaxItems    int
}

// CreateOrchestrateRun inserts a new orchestrate_runs row with status="running".
func (s *Store) CreateOrchestrateRun(ctx context.Context, p CreateOrchestrateRunParams) (string, error) {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO orchestrate_runs (id, backlog_size, max_items, done, failed, items, status)
		VALUES ($1, $2, $3, 0, 0, '[]', 'running')`,
		p.ID, p.BacklogSize, p.MaxItems)
	if err != nil {
		return "", fmt.Errorf("create orchestrate run: %w", err)
	}
	return p.ID, nil
}

// UpdateOrchestrateRun updates progress counters, item results, and status.
func (s *Store) UpdateOrchestrateRun(ctx context.Context, id string, done, failed int, items json.RawMessage, status string) error {
	if len(items) == 0 {
		items = json.RawMessage("[]")
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE orchestrate_runs
		SET done=$2, failed=$3, items=$4, status=$5
		WHERE id=$1`,
		id, done, failed, items, status)
	if err != nil {
		return fmt.Errorf("update orchestrate run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListOrchestrateRuns returns all orchestrate_runs, newest first.
func (s *Store) ListOrchestrateRuns(ctx context.Context) ([]OrchestrateRunRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, started_at, backlog_size, max_items, done, failed, items, status
		FROM orchestrate_runs ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list orchestrate runs: %w", err)
	}
	defer rows.Close()
	out := []OrchestrateRunRecord{}
	for rows.Next() {
		var r OrchestrateRunRecord
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.BacklogSize, &r.MaxItems,
			&r.Done, &r.Failed, &r.Items, &r.Status); err != nil {
			return nil, fmt.Errorf("scan orchestrate run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetOrchestrateRun returns a single orchestrate_runs record.
func (s *Store) GetOrchestrateRun(ctx context.Context, id string) (*OrchestrateRunRecord, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, started_at, backlog_size, max_items, done, failed, items, status
		FROM orchestrate_runs WHERE id=$1`, id)
	var rec OrchestrateRunRecord
	err := row.Scan(
		&rec.ID, &rec.StartedAt, &rec.BacklogSize, &rec.MaxItems,
		&rec.Done, &rec.Failed, &rec.Items, &rec.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get orchestrate run: %w", err)
	}
	return &rec, nil
}
