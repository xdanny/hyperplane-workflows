package store

import (
	"context"
	"testing"
)

func TestRecipeNamerUsesSDKTemplateNames(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()
	template, err := st.CreateTemplate(ctx, WorkflowTemplate{Name: "sdk-workflow-" + uniq(), Spec: JSON(`{"kind":"nodes","instruction":"Produce a report","nodes":[{"id":"report","title":"Report","instruction":"Write a report"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	namer, err := st.NewRecipeNamer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := namer.Name("  "+template.ID+" "), displayRecipeName(template.Name); got != want {
		t.Fatalf("name=%q, want %q", got, want)
	}

	if got := (*RecipeNamer)(nil).Name(template.ID); got != "" {
		t.Fatalf("nil namer=%q", got)
	}
}
