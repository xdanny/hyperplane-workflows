package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Decision is an ADR-style decision record: a first-class, self-organizing unit
// of knowledge. refs are edges to related decisions/entities that feed the
// knowledge graph.
type Decision struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Context      string   `json:"context,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	Why          string   `json:"why,omitempty"`
	Alternatives string   `json:"alternatives,omitempty"`
	Consequences string   `json:"consequences,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Refs         []string `json:"refs,omitempty"`
	Project      string   `json:"project,omitempty"`
	Source       string   `json:"source,omitempty"`
	// DecidedBy names who recorded the verdict. A person at this keyboard is
	// "local" (the default, since Hyperplane has no auth to name anyone else);
	// an acceptance the server recorded from a workflow's sign-off policy is
	// DecidedByAuto, which surfaces label as automatic and skill extraction
	// refuses to treat as a person's acceptance.
	DecidedBy string     `json:"decidedBy,omitempty"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

const decisionCols = `id, title, status, context, decision, why, alternatives, consequences, tags, refs, project, source, decided_by, decided_at, created_at, updated_at`

// DecidedByLocal is the one keyboard this local-first build can honestly name.
// DecidedByAuto marks an acceptance the server recorded itself, under a saved
// workflow's sign-off policy. It is never a person's decision.
const (
	DecidedByLocal = "local"
	DecidedByAuto  = "hyperplane:auto"
)

// decidedByOrLocal defaults an unspecified decider to the local person.
func decidedByOrLocal(by string) string {
	if strings.TrimSpace(by) == "" {
		return DecidedByLocal
	}
	return by
}

func scanDecision(row pgx.Row) (Decision, error) {
	var d Decision
	err := row.Scan(&d.ID, &d.Title, &d.Status, &d.Context, &d.Decision, &d.Why,
		&d.Alternatives, &d.Consequences, &d.Tags, &d.Refs, &d.Project, &d.Source,
		&d.DecidedBy, &d.DecidedAt, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// ListDecisions returns decision records (newest first), optionally scoped to a
// project. nil returns all decisions; a non-nil pointer (including one to "")
// filters to decisions whose project equals *project.
func (s *Store) ListDecisions(ctx context.Context, project *string) ([]Decision, error) {
	q := `SELECT ` + decisionCols + ` FROM decisions`
	args := []any{}
	if project != nil {
		q += ` WHERE project = $1`
		args = append(args, *project)
	}
	q += ` ORDER BY COALESCE(decided_at, updated_at) DESC, id DESC`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ProjectCount, ListProjects and the rest of the project surface live in
// projects.go now that a project is a first-class row rather than a tag
// aggregate.

// GetDecision fetches one decision by id.
func (s *Store) GetDecision(ctx context.Context, id string) (Decision, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+decisionCols+` FROM decisions WHERE id=$1`, id)
	d, err := scanDecision(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// UpsertDecision inserts or updates a decision keyed on id, returning the stored
// row. Used by both the API and the DECISIONS.md importer (idempotent).
func (s *Store) UpsertDecision(ctx context.Context, d Decision) (Decision, error) {
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if d.Refs == nil {
		d.Refs = []string{}
	}
	// Auto-register: a decision tagged with a project the table has never seen
	// gives that project a row, so the Projects surface never lags reality.
	if err := s.EnsureProject(ctx, d.Project); err != nil {
		return Decision{}, err
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO decisions
			(id, title, status, context, decision, why, alternatives, consequences, tags, refs, project, source, decided_by, decided_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET
			title        = EXCLUDED.title,
			status       = EXCLUDED.status,
			context      = EXCLUDED.context,
			decision     = EXCLUDED.decision,
			why          = EXCLUDED.why,
			alternatives = EXCLUDED.alternatives,
			consequences = EXCLUDED.consequences,
			tags         = EXCLUDED.tags,
			refs         = EXCLUDED.refs,
			project      = EXCLUDED.project,
			source       = EXCLUDED.source,
			decided_by   = EXCLUDED.decided_by,
			decided_at   = EXCLUDED.decided_at,
			updated_at   = now()
		RETURNING `+decisionCols,
		d.ID, d.Title, statusOrAccepted(d.Status), d.Context, d.Decision, d.Why,
		d.Alternatives, d.Consequences, d.Tags, d.Refs, d.Project, d.Source, decidedByOrLocal(d.DecidedBy), d.DecidedAt)
	out, err := scanDecision(row)
	if err != nil {
		return out, fmt.Errorf("upsert decision %q: %w", d.ID, err)
	}
	return out, nil
}

// DeleteDecision removes a decision record by id.
func (s *Store) DeleteDecision(ctx context.Context, id string) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM decisions WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete decision: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// statusOrAccepted defaults an empty status to "accepted".
func statusOrAccepted(s string) string {
	if s == "" {
		return "accepted"
	}
	return s
}
