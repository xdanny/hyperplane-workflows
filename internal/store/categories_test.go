package store

import (
	"context"
	"testing"
)

func TestCategoryCountsSDKTemplatesAndSkills(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()
	name := "sdk-category-" + uniq()
	if err := st.UpsertSkill(ctx, Skill{ID: name, Name: name, Category: name, Instruction: "Review the report"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTemplate(ctx, WorkflowTemplate{Name: name, Spec: JSON(`{"kind":"nodes","instruction":"Write a report","nodes":[{"id":"report","title":"Report","instruction":"Write a report"}]}`)}); err != nil {
		t.Fatal(err)
	}
	// Retired rows remain in migration-owned storage, but cannot inflate counts.
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workflow_drafts(name,spec) VALUES($1,'{}')`, name); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind string
		want int
	}{{"skills", 1}, {"workflows", 1}, {"all", 2}, {"processes", 0}} {
		got, err := st.categoryCount(ctx, Category{Name: name, Kind: tc.kind})
		if err != nil || got != tc.want {
			t.Fatalf("%s count=%d err=%v want=%d", tc.kind, got, err, tc.want)
		}
	}
}
