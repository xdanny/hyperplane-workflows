package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// MemoryItem is a platform-level typed fact that accumulates over time.
// Memory items are platform-wide and
// always injected into compose + agent node system prompts.
type MemoryItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`            // "user-pref" | "project-fact" | "feedback" | "reference"
	Key       string    `json:"key"`             // stable identifier
	Value     JSON      `json:"value"`           // typed payload
	Source    string    `json:"source"`          // "human" | "agent" | "optimize-loop"
	RunID     string    `json:"runId,omitempty"` // optional originating run
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// newMemoryID generates a time-based unique id for a MemoryItem.
func newMemoryID() string {
	return time.Now().Format("20060102-150405.000000") + "-mem"
}

// UpsertMemory inserts or updates a memory item keyed by its Key field.
// If item.ID is empty and the key is new, a new id is generated.
func (s *Store) UpsertMemory(ctx context.Context, item MemoryItem) error {
	if item.ID == "" {
		item.ID = newMemoryID()
	}
	if item.Source == "" {
		item.Source = "agent"
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO memory_items (id, kind, key, value, source, run_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,''), now(), now())
		ON CONFLICT (key) DO UPDATE SET
			kind       = EXCLUDED.kind,
			value      = EXCLUDED.value,
			source     = EXCLUDED.source,
			run_id     = EXCLUDED.run_id,
			updated_at = now()`,
		item.ID, item.Kind, item.Key, jsonOr(item.Value), item.Source, item.RunID)
	if err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}
	return nil
}

func scanMemory(row pgx.Row) (MemoryItem, error) {
	var m MemoryItem
	var runID *string
	err := row.Scan(&m.ID, &m.Kind, &m.Key, &m.Value, &m.Source, &runID, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return m, err
	}
	m.RunID = deref(runID)
	return m, nil
}

const memoryCols = `id, kind, key, value, source, run_id, created_at, updated_at`

// GetMemory fetches a single memory item by key.
func (s *Store) GetMemory(ctx context.Context, key string) (MemoryItem, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+memoryCols+` FROM memory_items WHERE key=$1`, key)
	m, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// ListMemory returns memory items, optionally filtered by kind, newest-updated
// first. When kind is empty, all kinds are returned.
func (s *Store) ListMemory(ctx context.Context, kind string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows pgx.Rows
	var err error
	if kind != "" {
		rows, err = s.Pool.Query(ctx,
			`SELECT `+memoryCols+` FROM memory_items WHERE kind=$1 ORDER BY updated_at DESC LIMIT $2`,
			kind, limit)
	} else {
		rows, err = s.Pool.Query(ctx,
			`SELECT `+memoryCols+` FROM memory_items ORDER BY updated_at DESC LIMIT $1`,
			limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list memory: %w", err)
	}
	defer rows.Close()
	var out []MemoryItem
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMemory removes a memory item by key. Returns ErrNotFound when absent.
func (s *Store) DeleteMemory(ctx context.Context, key string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM memory_items WHERE key=$1`, key)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
