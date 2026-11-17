package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrSkillChanged means an evaluated snapshot is no longer current. A caller
// must evaluate the new skill before applying another proposed instruction.
var ErrSkillChanged = errors.New("skill changed or was removed during evaluation; refresh and evaluate the current skill")

// ApplySkillInstruction updates only the instruction, and only while every
// field of the evaluated skill still matches. Model calls happen before this
// single conditional statement; no database lock spans model execution.
func (s *Store) ApplySkillInstruction(ctx context.Context, expected Skill, instruction string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE skills SET instruction=$9,updated_at=clock_timestamp()
 WHERE id=$1 AND updated_at=$2 AND instruction=$3 AND metadata=$4::jsonb
 AND name=$5 AND COALESCE(description,'')=$6 AND COALESCE(category,'')=$7 AND COALESCE(reasoning,'')=$8`,
		expected.ID, expected.UpdatedAt, expected.Instruction, jsonOr(expected.Metadata), expected.Name, expected.Description, expected.Category, expected.Reasoning, instruction)
	if err != nil {
		return fmt.Errorf("apply evaluated skill instruction: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillChanged
	}
	return nil
}
