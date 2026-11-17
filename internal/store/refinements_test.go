package store

import (
	"context"
	"testing"
)

func TestSkillRefinementsLineage(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	sk := "skill-refine-test"

	rows := []SkillRefinement{
		{ID: "ref-1", SkillID: sk, OptimizeRunID: "opt-1", Role: "baseline", Instruction: "v0", PassRate: 0.5, MeanScore: 0.55},
		{ID: "ref-2", SkillID: sk, OptimizeRunID: "opt-1", Role: "variant", Instruction: "v1", PassRate: 0.5, MeanScore: 0.60},
		{ID: "ref-3", SkillID: sk, OptimizeRunID: "opt-1", Role: "winner", Instruction: "v2", PassRate: 0.9, MeanScore: 0.88, Gain: 0.4, Applied: true},
	}
	for _, r := range rows {
		if err := st.CreateSkillRefinement(ctx, r); err != nil {
			t.Fatalf("create %s: %v", r.ID, err)
		}
	}

	got, err := st.ListSkillRefinements(ctx, sk)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 refinements, got %d", len(got))
	}
	// Find the winner and check its score + applied flag round-tripped.
	var winner *SkillRefinement
	for i := range got {
		if got[i].Role == "winner" {
			winner = &got[i]
		}
	}
	if winner == nil {
		t.Fatal("no winner refinement returned")
	}
	if winner.PassRate != 0.9 || !winner.Applied || winner.Gain != 0.4 {
		t.Errorf("winner score/applied/gain not persisted: %+v", *winner)
	}
	if st2, _ := st.ListSkillRefinements(ctx, "no-such-skill"); len(st2) != 0 {
		t.Errorf("expected 0 refinements for unknown skill, got %d", len(st2))
	}
}
