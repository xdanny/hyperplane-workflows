package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Category groups skills or SDK workflow templates under a named label.
type Category struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Count     int       `json:"count,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.CreatedAt)
	return c, err
}

const categoryCols = `id, name, kind, created_at`

// ListCategories returns all categories with live counts against skills and SDK workflow templates.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+categoryCols+` FROM categories ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Enrich with live counts.
	for i, c := range out {
		n, err := s.categoryCount(ctx, c)
		if err == nil {
			out[i].Count = n
		}
	}
	return out, nil
}

// categoryCount returns the number of items belonging to category c.
func (s *Store) categoryCount(ctx context.Context, c Category) (int, error) {
	switch c.Kind {
	case "skills":
		var n int
		err := s.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM skills WHERE category ILIKE $1`, c.Name,
		).Scan(&n)
		return n, err
	case "workflows":
		var n int
		err := s.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM workflow_templates WHERE name ILIKE '%'||$1||'%'`, c.Name,
		).Scan(&n)
		return n, err
	case "all":
		var n int
		err := s.Pool.QueryRow(ctx, `SELECT
            (SELECT COUNT(*) FROM skills WHERE category ILIKE $1) +
            (SELECT COUNT(*) FROM workflow_templates WHERE name ILIKE '%'||$1||'%')`, c.Name).Scan(&n)
		return n, err
	default:
		return 0, nil
	}

}

// GetCategory fetches one category by id.
func (s *Store) GetCategory(ctx context.Context, id string) (Category, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+categoryCols+` FROM categories WHERE id=$1`, id)
	c, err := scanCategory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// CreateCategory inserts a new category and returns the stored record.
func (s *Store) CreateCategory(ctx context.Context, c Category) (Category, error) {
	var out Category
	if c.Kind == "" {
		c.Kind = "all"
	}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO categories (name, kind)
		VALUES ($1, $2)
		RETURNING `+categoryCols,
		c.Name, c.Kind,
	).Scan(&out.ID, &out.Name, &out.Kind, &out.CreatedAt)
	if err != nil {
		return out, fmt.Errorf("create category: %w", err)
	}
	return out, nil
}
