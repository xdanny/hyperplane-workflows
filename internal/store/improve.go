package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ImproveRun is the DB record for one always-on self-improvement loop invocation.
type ImproveRun struct {
	ID               string          `json:"id"`
	StartedAt        time.Time       `json:"startedAt"`
	SkillsConsidered int             `json:"skillsConsidered"`
	SkillsOptimized  int             `json:"skillsOptimized"`
	SkillsPromoted   int             `json:"skillsPromoted"`
	Items            json.RawMessage `json:"items"`
	Status           string          `json:"status"`
}

// CreateImproveRun inserts a new improve_runs row with status="running".
func (s *Store) CreateImproveRun(ctx context.Context, run ImproveRun) (string, error) {
	items := run.Items
	if len(items) == 0 {
		items = json.RawMessage("[]")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO improve_runs (id, skills_considered, skills_optimized, skills_promoted, items, status)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		run.ID,
		run.SkillsConsidered,
		run.SkillsOptimized,
		run.SkillsPromoted,
		items,
		run.Status,
	)
	if err != nil {
		return "", fmt.Errorf("create improve run: %w", err)
	}
	return run.ID, nil
}

// UpdateImproveRun updates counters, items, and status for an existing improve_runs row.
func (s *Store) UpdateImproveRun(ctx context.Context, run ImproveRun) error {
	items := run.Items
	if len(items) == 0 {
		items = json.RawMessage("[]")
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE improve_runs
		SET skills_considered=$2, skills_optimized=$3, skills_promoted=$4, items=$5, status=$6
		WHERE id=$1`,
		run.ID,
		run.SkillsConsidered,
		run.SkillsOptimized,
		run.SkillsPromoted,
		items,
		run.Status,
	)
	if err != nil {
		return fmt.Errorf("update improve run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListImproveRuns returns all improve_runs, newest first.
func (s *Store) ListImproveRuns(ctx context.Context) ([]ImproveRun, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, started_at, skills_considered, skills_optimized, skills_promoted, items, status
		FROM improve_runs ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list improve runs: %w", err)
	}
	defer rows.Close()
	out := []ImproveRun{}
	for rows.Next() {
		var r ImproveRun
		if err := rows.Scan(
			&r.ID, &r.StartedAt,
			&r.SkillsConsidered, &r.SkillsOptimized, &r.SkillsPromoted,
			&r.Items, &r.Status,
		); err != nil {
			return nil, fmt.Errorf("scan improve run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetImproveRun returns a single improve_runs record.
func (s *Store) GetImproveRun(ctx context.Context, id string) (*ImproveRun, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, started_at, skills_considered, skills_optimized, skills_promoted, items, status
		FROM improve_runs WHERE id=$1`, id)
	var rec ImproveRun
	err := row.Scan(
		&rec.ID, &rec.StartedAt,
		&rec.SkillsConsidered, &rec.SkillsOptimized, &rec.SkillsPromoted,
		&rec.Items, &rec.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get improve run: %w", err)
	}
	return &rec, nil
}
