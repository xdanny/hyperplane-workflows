package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stack is a reusable technology stack definition.
type Stack struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Runtime     string    `json:"runtime,omitempty"`
	Dockerized  bool      `json:"dockerized"`
	Notes       string    `json:"notes,omitempty"`
	Files       JSON      `json:"files,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

func scanStack(row pgx.Row) (Stack, error) {
	var st Stack
	var desc, runtime, notes *string
	err := row.Scan(&st.ID, &st.Name, &desc, &runtime, &st.Dockerized, &notes, &st.Files, &st.CreatedAt)
	if err != nil {
		return st, err
	}
	st.Description = deref(desc)
	st.Runtime = deref(runtime)
	st.Notes = deref(notes)
	return st, nil
}

const stackCols = `id, name, description, runtime, dockerized, notes, files, created_at`

// ListStacks returns all stacks ordered by name.
func (s *Store) ListStacks(ctx context.Context) ([]Stack, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+stackCols+` FROM stacks ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list stacks: %w", err)
	}
	defer rows.Close()
	var out []Stack
	for rows.Next() {
		st, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// GetStack fetches one stack by id.
func (s *Store) GetStack(ctx context.Context, id string) (Stack, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+stackCols+` FROM stacks WHERE id=$1`, id)
	st, err := scanStack(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, err
}

// CreateStack inserts a new stack and returns the stored record.
func (s *Store) CreateStack(ctx context.Context, st Stack) (Stack, error) {
	var out Stack
	var desc, runtime, notes *string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO stacks (name, description, runtime, dockerized, notes, files)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, NULLIF($5,''), $6)
		RETURNING `+stackCols,
		st.Name, st.Description, st.Runtime, st.Dockerized, st.Notes, jsonOr(st.Files),
	).Scan(&out.ID, &out.Name, &desc, &runtime, &out.Dockerized, &notes, &out.Files, &out.CreatedAt)
	if err != nil {
		return out, fmt.Errorf("create stack: %w", err)
	}
	out.Description = deref(desc)
	out.Runtime = deref(runtime)
	out.Notes = deref(notes)
	return out, nil
}
