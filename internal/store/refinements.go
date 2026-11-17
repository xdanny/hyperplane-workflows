package store

import (
	"context"
	"fmt"
	"time"
)

// SkillRefinement is one prompt refinement (baseline / variant / winner) tried by
// the optimize loop, with its eval score. The set of refinements for a skill is its
// improvement lineage — see migration 0013.
type SkillRefinement struct {
	ID            string    `json:"id"`
	SkillID       string    `json:"skillId"`
	OptimizeRunID string    `json:"optimizeRunId,omitempty"`
	Role          string    `json:"role"` // baseline | variant | winner
	Instruction   string    `json:"instruction"`
	PassRate      float64   `json:"passRate"`
	MeanScore     float64   `json:"meanScore"`
	Gain          float64   `json:"gain"`
	Applied       bool      `json:"applied"`
	CreatedAt     time.Time `json:"createdAt"`
}

// CreateSkillRefinement inserts one refinement record.
func (s *Store) CreateSkillRefinement(ctx context.Context, r SkillRefinement) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO skill_refinements
			(id, skill_id, optimize_run_id, role, instruction, pass_rate, mean_score, gain, applied)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, r.SkillID, nullStr(r.OptimizeRunID), r.Role, r.Instruction,
		r.PassRate, r.MeanScore, r.Gain, r.Applied)
	if err != nil {
		return fmt.Errorf("create skill refinement: %w", err)
	}
	return nil
}

// ListSkillRefinements returns a skill's refinement lineage, newest first.
func (s *Store) ListSkillRefinements(ctx context.Context, skillID string) ([]SkillRefinement, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, skill_id, COALESCE(optimize_run_id,''), role, instruction,
		       pass_rate, mean_score, gain, applied, created_at
		FROM skill_refinements WHERE skill_id=$1 ORDER BY created_at DESC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill refinements: %w", err)
	}
	defer rows.Close()
	out := []SkillRefinement{}
	for rows.Next() {
		var r SkillRefinement
		if err := rows.Scan(&r.ID, &r.SkillID, &r.OptimizeRunID, &r.Role, &r.Instruction,
			&r.PassRate, &r.MeanScore, &r.Gain, &r.Applied, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan skill refinement: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// nullStr returns nil for an empty string so it stores as SQL NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
