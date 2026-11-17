package store

// Outcome briefs use database insertion order for recency (migration 0030).
//
// A brief is the platform's own plain-language account of what a run, a
// delivery or a project actually produced, written AFTER the subject reached a
// terminal state. Insert-only, latest-wins for display; timestamps are display
// metadata and never decide which account of a subject is current.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Subject kinds a brief can be written about.
const (
	// BriefSubjectRun is one run's node-graph outcome.
	BriefSubjectRun = "run"
	// BriefSubjectDelivery is the human's recorded verdict on a finished run.
	BriefSubjectDelivery = "delivery"
	// BriefSubjectProject is "what this project has built so far".
	BriefSubjectProject = "project"
)

// Who wrote a brief.
const (
	// BriefEngineCodex — the headless engine wrote it from the state document.
	BriefEngineCodex = "codex"
	// BriefEngineFallback — the platform wrote it deterministically, because the
	// engine was unavailable, disabled, or failed twice. A fallback brief says
	// less; it never says something the state does not.
	BriefEngineFallback = "fallback"
)

// Brief is one recorded outcome brief.
type Brief struct {
	ID          string `json:"id"`
	SubjectKind string `json:"subjectKind"`
	SubjectID   string `json:"subjectId"`
	Project     string `json:"project,omitempty"`
	// Body is the four-part brief as a JSON document (see internal/brief).
	Body string `json:"body"`
	// Grounded reports that every number in Body was found in the state
	// document the brief was written from. False means the brief is still shown
	// — and badged.
	Grounded bool `json:"grounded"`
	// UngroundedNumbers lists the offending numeric tokens, SPACE-separated:
	// a numeric token can itself contain a comma ("9,412"), so a comma would
	// not survive the round trip.
	UngroundedNumbers string    `json:"ungroundedNumbers,omitempty"`
	Engine            string    `json:"engine"`
	InputsDigest      string    `json:"inputsDigest,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

const briefCols = `id, subject_kind, subject_id, project, body, grounded, ungrounded_numbers, engine, inputs_digest, created_at`

func scanBrief(row pgx.Row) (Brief, error) {
	var b Brief
	err := row.Scan(&b.ID, &b.SubjectKind, &b.SubjectID, &b.Project, &b.Body,
		&b.Grounded, &b.UngroundedNumbers, &b.Engine, &b.InputsDigest, &b.CreatedAt)
	return b, err
}

// InsertBrief records a brief. Insert-only: a taken id is ErrExists rather than
// an overwrite, so a retry cannot silently replace the account of a subject.
func (s *Store) InsertBrief(ctx context.Context, b Brief) (Brief, error) {
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO briefs (id, subject_kind, subject_id, project, body, grounded, ungrounded_numbers, engine, inputs_digest)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+briefCols,
		b.ID, b.SubjectKind, b.SubjectID, b.Project, b.Body, b.Grounded,
		b.UngroundedNumbers, b.Engine, b.InputsDigest)
	out, err := scanBrief(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrExists
	}
	if err != nil {
		return out, fmt.Errorf("insert brief %q: %w", b.ID, err)
	}
	return out, nil
}

// LatestBrief returns the last inserted brief for a subject, or ErrNotFound.
func (s *Store) LatestBrief(ctx context.Context, kind, subjectID string) (Brief, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT `+briefCols+` FROM briefs
		WHERE subject_kind=$1 AND subject_id=$2
		ORDER BY ordinal DESC
		LIMIT 1`, kind, subjectID)
	b, err := scanBrief(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, fmt.Errorf("latest brief %s/%s: %w", kind, subjectID, err)
	}
	return b, nil
}

// ListBriefs returns a project's briefs of one kind, last inserted first, capped by
// limit (0 ⇒ 20). It is what the project narrative is regenerated from: the
// latest accepted deliveries, in the platform's own words.
//
// Only the last inserted brief per subject is returned, because a subject that was
// re-briefed after a repair would otherwise contribute its own history twice.
func (s *Store) ListBriefs(ctx context.Context, project, kind string, limit int) ([]Brief, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+briefCols+` FROM (
			SELECT DISTINCT ON (subject_id) `+briefCols+`, ordinal FROM briefs
			WHERE project=$1 AND subject_kind=$2
			ORDER BY subject_id, ordinal DESC
		) latest
		ORDER BY ordinal DESC LIMIT $3`, project, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("list briefs: %w", err)
	}
	defer rows.Close()

	var out []Brief
	for rows.Next() {
		b, err := scanBrief(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
