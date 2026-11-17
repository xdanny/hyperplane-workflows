package store

// personas.go — the registry half of first-class personas.
//
// A persona row is a REGISTERED CHARTER: the markdown file in personas/ is the
// source of truth, and this table is the platform's copy of it, refreshed by
// `make seed` exactly the way templates and trusted processes are. There is no
// write API on purpose — a charter is amended in git, in its own PR, because
// changing IDENTITY or RULES invalidates comparison with past runs.
//
// Reviews are NOT a new table. A persona review is a record
// (type='persona-review'), insert-only, one row per review — see
// internal/persona for why multiple reviews of the same target are kept rather
// than overwritten.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PersonaReviewRecordType is the records-table bucket holding persona reviews.
const PersonaReviewRecordType = "persona-review"

// Persona is a registered audit charter.
type Persona struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Bar is the reviewer's own one-line standard ("Is this evidence, or is it
	// eval theater?"). It is lifted from the charter rather than written here,
	// so a card can show what the reviewer is FOR without paraphrasing them.
	Bar string `json:"bar,omitempty"`
	// Charter is the full markdown, verbatim. The review prompt is assembled
	// from its own sections, so the file stays the brief.
	Charter string `json:"charter,omitempty"`
	// Competence is the parsed COMPETENCE section (see persona.Competence).
	Competence JSON `json:"competence,omitempty"`
	// Version is a content hash of the charter — what makes "this verdict was
	// given against charter v3" a checkable statement.
	Version   string    `json:"version,omitempty"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

const personaCols = `id, name, bar, charter, competence, version, source, created_at, updated_at`

func scanPersona(row pgx.Row) (Persona, error) {
	var p Persona
	err := row.Scan(&p.ID, &p.Name, &p.Bar, &p.Charter, &p.Competence,
		&p.Version, &p.Source, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// UpsertPersona registers a charter or refreshes its content.
//
// Idempotent by design: re-running the seed over an unchanged file rewrites the
// same values, and a charter amended in git lands on the next seed with a new
// version hash. created_at is never touched, so "registered since" survives a
// re-import.
func (s *Store) UpsertPersona(ctx context.Context, p Persona) error {
	if p.ID == "" {
		return fmt.Errorf("upsert persona: id is required")
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO personas (id, name, bar, charter, competence, version, source, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now(),now())
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, bar=EXCLUDED.bar, charter=EXCLUDED.charter,
			competence=EXCLUDED.competence, version=EXCLUDED.version,
			source=EXCLUDED.source, updated_at=now()`,
		p.ID, p.Name, p.Bar, p.Charter, jsonOr(p.Competence), p.Version, p.Source)
	if err != nil {
		return fmt.Errorf("upsert persona %q: %w", p.ID, err)
	}
	return nil
}

// ListPersonas returns every registered charter, by name.
func (s *Store) ListPersonas(ctx context.Context) ([]Persona, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+personaCols+` FROM personas ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPersona fetches one charter.
func (s *Store) GetPersona(ctx context.Context, id string) (Persona, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+personaCols+` FROM personas WHERE id=$1`, id)
	p, err := scanPersona(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ListPersonaReviews returns persona-review records, newest first. An empty
// target returns every review; a target ("skill:foo" / "run:2026…") returns the
// reviews of exactly that thing.
//
// Reviews are insert-only and several may exist for one target — that is the
// point: two personas disagreeing, or the same persona re-reviewing after a
// change, is evidence a human wants both halves of. limit <= 0 means "the
// default cap"; nothing here is ever unbounded.
func (s *Store) ListPersonaReviews(ctx context.Context, target string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT ` + recordCols + ` FROM records WHERE type=$1`
	args := []any{PersonaReviewRecordType}
	if target != "" {
		q += ` AND doc->>'target' = $2`
		args = append(args, target)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d`, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list persona reviews: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountPersonaReviews counts the reviews one persona has recorded against one
// target. It is what gives an insert-only review id its sequence number, so a
// second review never collides with (or overwrites) the first.
func (s *Store) CountPersonaReviews(ctx context.Context, personaID, target string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM records
		WHERE type=$1 AND doc->>'persona' = $2 AND doc->>'target' = $3`,
		PersonaReviewRecordType, personaID, target).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count persona reviews: %w", err)
	}
	return n, nil
}

// DecodePersonaCompetence unmarshals a persona's competence blob into v.
// Convenience so callers do not each re-derive the empty-blob case.
func DecodePersonaCompetence(raw JSON, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}
