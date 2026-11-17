package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ---- Skills ---------------------------------------------------------------

// Skill lifecycle statuses. A skill's status lives in its metadata jsonb — no
// column, no migration — because it is lifecycle bookkeeping, not schema:
// extraction from a run writes "draft", human approval writes "active".
// A skill with NO status is active: every seeded or hand-authored skill
// predates extraction and must keep working untouched.
const (
	SkillStatusDraft  = "draft"
	SkillStatusActive = "active"
)

// SkillStatus returns the skill's lifecycle status, defaulting to active.
func SkillStatus(sk Skill) string {
	if len(sk.Metadata) == 0 {
		return SkillStatusActive
	}
	var meta struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(sk.Metadata, &meta) != nil || meta.Status == "" {
		return SkillStatusActive
	}
	return meta.Status
}

// IsDraftSkill reports whether a skill is an unapproved draft. Drafts are
// never injected into a codex prompt — a person approves them first.
func IsDraftSkill(sk Skill) bool { return SkillStatus(sk) == SkillStatusDraft }

// sqlSkillActive is the SQL twin of !IsDraftSkill: a skill is a draft only when
// its metadata status is exactly "draft"; a missing, null or empty status is
// active, so every seeded skill (which has none) stays resolvable. Keep this in
// lockstep with SkillStatus — it is the same rule, evaluated in Postgres.
const sqlSkillActive = `metadata->>'status' IS DISTINCT FROM '` + SkillStatusDraft + `'`

// ErrConflict marks a write that was refused because it would have overwritten
// an existing row. It is the insert-only counterpart to ErrNotFound.
var ErrConflict = errors.New("already exists")

// UpsertSkill inserts or updates a skill.
func (s *Store) UpsertSkill(ctx context.Context, sk Skill) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO skills (id, name, description, category, reasoning, instruction, metadata, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, category=EXCLUDED.category,
			reasoning=EXCLUDED.reasoning, instruction=EXCLUDED.instruction,
			metadata=EXCLUDED.metadata, updated_at=now()`,
		sk.ID, sk.Name, sk.Description, sk.Category, sk.Reasoning, sk.Instruction, jsonOr(sk.Metadata))
	if err != nil {
		return fmt.Errorf("upsert skill: %w", err)
	}
	return nil
}

// InsertSkill inserts a skill and REFUSES to overwrite one that already exists,
// returning ErrConflict instead. UpsertSkill is the right call for an edit or an
// approval; this is the right call for creating something new — notably skill
// extraction, where an id collision means "a skill already covers this" and an
// upsert would silently replace an approved skill with a draft.
func (s *Store) InsertSkill(ctx context.Context, sk Skill) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO skills (id, name, description, category, reasoning, instruction, metadata, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (id) DO NOTHING`,
		sk.ID, sk.Name, sk.Description, sk.Category, sk.Reasoning, sk.Instruction, jsonOr(sk.Metadata))
	if err != nil {
		return fmt.Errorf("insert skill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("insert skill %q: %w", sk.ID, ErrConflict)
	}
	return nil
}

func scanSkill(row pgx.Row) (Skill, error) {
	var sk Skill
	var desc, cat, reason *string
	err := row.Scan(&sk.ID, &sk.Name, &desc, &cat, &reason, &sk.Instruction, &sk.Metadata, &sk.UpdatedAt)
	if err != nil {
		return sk, err
	}
	sk.Description = deref(desc)
	sk.Category = deref(cat)
	sk.Reasoning = deref(reason)
	return sk, nil
}

const skillCols = `id, name, description, category, reasoning, instruction, metadata, updated_at`

// GetSkill fetches one skill.
func (s *Store) GetSkill(ctx context.Context, id string) (Skill, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+skillCols+` FROM skills WHERE id=$1`, id)
	sk, err := scanSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return sk, ErrNotFound
	}
	return sk, err
}

// ListSkills returns all skills ordered by name.
func (s *Store) ListSkills(ctx context.Context) ([]Skill, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+skillCols+` FROM skills ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	return collectSkills(rows)
}

// SearchSkills does a fuzzy/substring search over name+description+category,
// ranked by trigram similarity. Empty query returns all (by name).
func (s *Store) SearchSkills(ctx context.Context, query string, limit int) ([]Skill, error) {
	if query == "" {
		return s.ListSkills(ctx)
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+skillCols+` FROM skills
		WHERE search ILIKE '%'||$1||'%' OR search % $1
		ORDER BY similarity(search, $1) DESC, name
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search skills: %w", err)
	}
	return collectSkills(rows)
}

// SearchActiveSkills is SearchSkills restricted to APPROVED skills. The status
// filter runs in SQL rather than over the returned page, so a caller that wants
// "the best usable skill for this query" is never bounded by drafts eating slots
// in the result window — which is what an over-fetch-then-filter-in-Go dance
// silently does. Empty query returns every active skill, by name.
func (s *Store) SearchActiveSkills(ctx context.Context, query string, limit int) ([]Skill, error) {
	if query == "" {
		rows, err := s.Pool.Query(ctx,
			`SELECT `+skillCols+` FROM skills WHERE `+sqlSkillActive+` ORDER BY name`)
		if err != nil {
			return nil, fmt.Errorf("list active skills: %w", err)
		}
		return collectSkills(rows)
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+skillCols+` FROM skills
		WHERE (search ILIKE '%'||$1||'%' OR search % $1) AND `+sqlSkillActive+`
		ORDER BY similarity(search, $1) DESC, name
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search active skills: %w", err)
	}
	return collectSkills(rows)
}

// ListLearnedDrafts returns the draft skills the platform PROPOSED itself:
// unapproved drafts that carry the run they were learned from. Newest first.
//
// The `sourceRun` filter is what makes this an inbox query rather than a skills
// query. A hand-authored draft somebody is still writing is not a proposal
// waiting on a verdict — it is unfinished work, and putting it in the queue
// would ask a question nobody asked for. Only a draft extraction created has a
// source run, so only those become proposal cards.
func (s *Store) ListLearnedDrafts(ctx context.Context, limit int) ([]Skill, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+skillCols+` FROM skills
		WHERE metadata->>'status' = $1
		  AND COALESCE(metadata->>'sourceRun', '') <> ''
		ORDER BY updated_at DESC
		LIMIT $2`, SkillStatusDraft, limit)
	if err != nil {
		return nil, fmt.Errorf("list learned drafts: %w", err)
	}
	return collectSkills(rows)
}

// DeleteSkill removes a skill. Returns ErrNotFound when no such skill exists.
// This is the "reject" half of the draft lifecycle: a rejected draft leaves no
// trace in the knowledge base.
func (s *Store) DeleteSkill(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM skills WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func collectSkills(rows pgx.Rows) ([]Skill, error) {
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// ---- Context bundles ------------------------------------------------------

// UpsertContextBundle inserts or updates a context bundle.
func (s *Store) UpsertContextBundle(ctx context.Context, b ContextBundle) error {
	items := b.Items
	if len(items) == 0 {
		items = JSON("[]")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO context_bundles (id, name, description, items, updated_at)
		VALUES ($1,$2,$3,$4,now())
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, items=EXCLUDED.items, updated_at=now()`,
		b.ID, b.Name, b.Description, items)
	if err != nil {
		return fmt.Errorf("upsert context bundle: %w", err)
	}
	return nil
}

// ListContextBundles returns all context bundles.
func (s *Store) ListContextBundles(ctx context.Context) ([]ContextBundle, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, description, items, updated_at FROM context_bundles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list context bundles: %w", err)
	}
	defer rows.Close()
	var out []ContextBundle
	for rows.Next() {
		var b ContextBundle
		var desc *string
		if err := rows.Scan(&b.ID, &b.Name, &desc, &b.Items, &b.UpdatedAt); err != nil {
			return nil, err
		}
		b.Description = deref(desc)
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetContextBundle returns a single context bundle by id.
func (s *Store) GetContextBundle(ctx context.Context, id string) (ContextBundle, error) {
	var b ContextBundle
	var desc *string
	err := s.Pool.QueryRow(ctx,
		`SELECT id, name, description, items, updated_at FROM context_bundles WHERE id=$1`, id,
	).Scan(&b.ID, &b.Name, &desc, &b.Items, &b.UpdatedAt)
	if err != nil {
		return ContextBundle{}, fmt.Errorf("get context bundle %s: %w", id, err)
	}
	b.Description = deref(desc)
	return b, nil
}

// ---- Preferences (DSPy/RLHF) ----------------------------------------------

// AppendPreference records a selection/judge preference.
func (s *Store) AppendPreference(ctx context.Context, p Preference) (int64, error) {
	cands := p.Candidates
	if len(cands) == 0 {
		cands = JSON("[]")
	}
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO preferences (skill_id, source, winner_id, candidates, run_id, created_at)
		VALUES ($1,$2,NULLIF($3,''),$4,NULLIF($5,''),now()) RETURNING id`,
		p.SkillID, p.Source, p.WinnerID, cands, p.RunID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append preference: %w", err)
	}
	return id, nil
}

// ListPreferences returns preferences for a skill, newest first.
func (s *Store) ListPreferences(ctx context.Context, skillID string) ([]Preference, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, skill_id, source, winner_id, candidates, run_id, created_at
		FROM preferences WHERE skill_id=$1 ORDER BY created_at DESC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list preferences: %w", err)
	}
	defer rows.Close()
	var out []Preference
	for rows.Next() {
		var p Preference
		var winner, runID *string
		if err := rows.Scan(&p.ID, &p.SkillID, &p.Source, &winner, &p.Candidates, &runID, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.WinnerID = deref(winner)
		p.RunID = deref(runID)
		out = append(out, p)
	}
	return out, rows.Err()
}
