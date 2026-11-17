package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TechRadar is a technology entry on the platform's tech radar.
type TechRadar struct {
	ID          string    `json:"id"`
	Tech        string    `json:"tech"`
	Quadrant    string    `json:"quadrant"`
	Ring        string    `json:"ring"`
	Note        string    `json:"note,omitempty"`
	Description string    `json:"description,omitempty"`
	SkillRef    string    `json:"skillRef,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func scanRadar(row pgx.Row) (TechRadar, error) {
	var r TechRadar
	var note, desc, skillRef *string
	err := row.Scan(&r.ID, &r.Tech, &r.Quadrant, &r.Ring, &note, &desc, &skillRef, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.Note = deref(note)
	r.Description = deref(desc)
	r.SkillRef = deref(skillRef)
	return r, nil
}

const radarCols = `id, tech, quadrant, ring, note, description, skill_ref, status, created_at, updated_at`

// ListTechRadar returns all active tech radar entries ordered by quadrant then ring then tech.
func (s *Store) ListTechRadar(ctx context.Context) ([]TechRadar, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT `+radarCols+` FROM tech_radar WHERE status != 'archived' ORDER BY quadrant, ring, tech`)
	if err != nil {
		return nil, fmt.Errorf("list tech radar: %w", err)
	}
	defer rows.Close()
	var out []TechRadar
	for rows.Next() {
		r, err := scanRadar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTechRadar fetches one tech radar entry by id.
func (s *Store) GetTechRadar(ctx context.Context, id string) (TechRadar, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+radarCols+` FROM tech_radar WHERE id=$1`, id)
	r, err := scanRadar(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// GetTechRadarEntry fetches one tech radar entry by id (alias for GetTechRadar).
func (s *Store) GetTechRadarEntry(ctx context.Context, id string) (TechRadar, error) {
	return s.GetTechRadar(ctx, id)
}

// CreateTechRadar inserts a new tech radar entry and returns the stored record.
func (s *Store) CreateTechRadar(ctx context.Context, t TechRadar) (TechRadar, error) {
	var out TechRadar
	var note, desc, skillRef *string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO tech_radar (tech, quadrant, ring, note, description, skill_ref, status)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), $7)
		RETURNING `+radarCols,
		t.Tech, t.Quadrant, t.Ring, t.Note, t.Description, t.SkillRef, statusOr(t.Status),
	).Scan(&out.ID, &out.Tech, &out.Quadrant, &out.Ring, &note, &desc, &skillRef, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return out, fmt.Errorf("create tech radar: %w", err)
	}
	out.Note = deref(note)
	out.Description = deref(desc)
	out.SkillRef = deref(skillRef)
	return out, nil
}

// UpsertTechRadarEntry inserts or updates a tech radar entry keyed on tech name.
// Existing entries are updated in-place; new entries are inserted.
func (s *Store) UpsertTechRadarEntry(ctx context.Context, t TechRadar) (TechRadar, error) {
	var out TechRadar
	var note, desc, skillRef *string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO tech_radar (tech, quadrant, ring, note, description, skill_ref, status)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), $7)
		ON CONFLICT (tech) DO UPDATE SET
			quadrant    = EXCLUDED.quadrant,
			ring        = EXCLUDED.ring,
			note        = EXCLUDED.note,
			description = EXCLUDED.description,
			skill_ref   = EXCLUDED.skill_ref,
			status      = EXCLUDED.status,
			updated_at  = now()
		RETURNING `+radarCols,
		t.Tech, t.Quadrant, t.Ring, t.Note, t.Description, t.SkillRef, statusOr(t.Status),
	).Scan(&out.ID, &out.Tech, &out.Quadrant, &out.Ring, &note, &desc, &skillRef, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return out, fmt.Errorf("upsert tech radar entry %q: %w", t.Tech, err)
	}
	out.Note = deref(note)
	out.Description = deref(desc)
	out.SkillRef = deref(skillRef)
	return out, nil
}

// statusOr returns "active" when the input is empty.
func statusOr(s string) string {
	if s == "" {
		return "active"
	}
	return s
}
