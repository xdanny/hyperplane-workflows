package store

// D-13's residuals and M-17: the three ways a declaration reached a run looking
// complete when it was not.
//
//  1. The tolerance registry had no `type` or `why`, so an inherited features
//     row arrived with two REQUIRED cells blank and the form said ready.
//  2. `ValidateDeclarations` carried the sentence "a unit with no value is not a
//     tolerance" and ran on no write path at all, so the row was stored anyway.
//  3. A holdout could be declared with no content hash and nothing said so
//     anywhere afterwards.

import (
	"strings"
	"testing"
)

func TestAToleranceCarriesItsTypeAndItsConsequence(t *testing.T) {
	decl, err := ParseDeclarations(JSON(`{"tolerances":[
	  {"subject":"payment_type","type":"categorical","why":"a new code silently breaks the card/cash split",
	   "statistic":"category-emergence","value":2,"unit":"points_of_share","severity":"major","owner":"Dev"}
	]}`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	got := PrefillSlots(decl)["features"].Value
	for _, want := range []string{
		`"type":"categorical"`,
		`"whyItMatters":"a new code silently breaks the card/cash split"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the inherited features row never carries %s:\n%s", want, got)
		}
	}
}

func TestValidationRefusesARowThatIsNotATolerance(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"a subject and a unit and no number",
			`{"tolerances":[{"subject":"PULocationID","unit":"points_of_share","owner":"Dev"}]}`,
			"a tolerance with no value is not a tolerance"},
		{"a subject and nothing else",
			`{"tolerances":[{"subject":"PULocationID","owner":"Dev"}]}`,
			"a tolerance with no value is not a tolerance"},
		{"a subject type nobody can compute",
			`{"tolerances":[{"subject":"x","value":1,"unit":"psi","type":"vibes"}]}`,
			"is not a subject type"},
		{"a lookup declared as the window under test",
			`{"datasets":[{"name":"zones","kind":"lookup","window":"current"}]}`,
			"it is never the data under test"},
		{"a role nobody has a word for",
			`{"datasets":[{"name":"zones","kind":"reference"}]}`,
			"is not a dataset role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			decl, err := ParseDeclarations(JSON(c.doc))
			if err != nil {
				t.Fatalf("ParseDeclarations: %v", err)
			}
			problems := ValidateDeclarations(decl)
			if len(problems) == 0 {
				t.Fatalf("nothing objected to %s", c.doc)
			}
			if !strings.Contains(strings.Join(problems, "; "), c.want) {
				t.Errorf("the refusal never says %q: %v", c.want, problems)
			}
		})
	}
	// `exact` still needs no number: "these must match" is a real tolerance.
	decl, _ := ParseDeclarations(JSON(`{"tolerances":[{"subject":"closed_deals","unit":"exact"}]}`))
	if p := ValidateDeclarations(decl); len(p) != 0 {
		t.Errorf("an exact tolerance was refused: %v", p)
	}
}

// TestASaveAnswersOnlyForWhatItIntroduced: validation had never run on the write
// path, so a project may already carry a row that would not be accepted today.
// Refusing an unrelated edit over it would lock the owner out of the one form
// where they could fix it.
func TestASaveAnswersOnlyForWhatItIntroduced(t *testing.T) {
	existing := JSON(`{"tolerances":[{"subject":"PULocationID","unit":"points_of_share"}]}`)

	// Editing another group entirely: the pre-existing row is not this save's
	// fault and does not block it.
	merged, err := MergeDeclarations(existing, JSON(`{"purpose":{"owner":"Sofia"}}`))
	if err != nil {
		t.Fatalf("MergeDeclarations: %v", err)
	}
	if err := refuseNewDeclarationProblems(existing, merged); err != nil {
		t.Errorf("an unrelated edit was refused over a row it did not add: %v", err)
	}

	// Adding a second bad row IS this save's fault.
	merged, err = MergeDeclarations(existing, JSON(
		`{"tolerances":[{"subject":"PULocationID","unit":"points_of_share"},{"subject":"trip_distance","unit":"psi"}]}`))
	if err != nil {
		t.Fatalf("MergeDeclarations: %v", err)
	}
	err = refuseNewDeclarationProblems(existing, merged)
	if err == nil {
		t.Fatal("a new tolerance with no number was accepted")
	}
	if !strings.Contains(err.Error(), "trip_distance") {
		t.Errorf("the refusal does not name the row this save added: %v", err)
	}
	if strings.Contains(err.Error(), "PULocationID") {
		t.Errorf("the refusal blames a row that was already there: %v", err)
	}
}

// TestAHoldoutWithNoHashIsAcceptedAndSaysSo. Refusing it would push the
// declaration back into memory; accepting it silently is what made D-1 look
// solved. The absence travels with the declaration instead.
func TestAHoldoutWithNoHashIsAcceptedAndSaysSo(t *testing.T) {
	decl, err := ParseDeclarations(JSON(
		`{"evalsets":[{"id":"june","kind":"holdout","location":"analytics.holdout","rowCount":96417}]}`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if p := ValidateDeclarations(decl); len(p) != 0 {
		t.Errorf("a holdout with no hash was refused: %v", p)
	}
	caveats := DeclarationCaveats(decl)
	if len(caveats) != 1 || !strings.Contains(caveats[0], NoHoldoutHashCaveat) {
		t.Fatalf("caveats = %v, want one naming the missing content hash", caveats)
	}
	if !strings.Contains(caveats[0], "june") {
		t.Errorf("the caveat does not name which evalset it is about: %q", caveats[0])
	}

	// A holdout that HAS one carries no caveat, and neither does a dev set.
	withHash, _ := ParseDeclarations(JSON(
		`{"evalsets":[{"id":"june","kind":"holdout","sha256":"9b74"},{"id":"dev","kind":"dev"}]}`))
	if c := DeclarationCaveats(withHash); len(c) != 0 {
		t.Errorf("caveats = %v, want none", c)
	}
}

// TestARequiredCellIsRequired is M-17: a table with rows is not a table that is
// filled in. The inherited features row arrived with Type and Why blank, the
// form said ready, and the recipe's gate refused the run one screen later.
func TestARequiredCellIsRequired(t *testing.T) {
	slot := ContextSlot{
		ID: "features", Required: true, Type: SlotTypeTable,
		Columns: []SlotColumn{
			{ID: "feature", Label: "Feature", Required: true},
			{ID: "type", Label: "Type", Required: true},
			{ID: "whyItMatters", Label: "Why it matters", Required: true},
			{ID: "owner", Label: "Set by"},
		},
	}
	slots := []ContextSlot{slot}

	complete := `[{"feature":"payment_type","type":"categorical","whyItMatters":"the card/cash split","owner":"Dev"}]`
	if m := MissingSlots(slots, map[string]string{"features": complete}); len(m) != 0 {
		t.Errorf("a complete row was reported missing: %v", m)
	}

	partial := `[{"feature":"PULocationID","owner":"Dev"}]`
	if m := MissingSlots(slots, map[string]string{"features": partial}); len(m) != 1 {
		t.Errorf("a row with two required cells empty counted as filled: %v", m)
	}
	problems := IncompleteTableRows(slot, partial)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want one per empty required cell", problems)
	}
	joined := strings.Join(problems, "; ")
	for _, want := range []string{"PULocationID", `"Type"`, `"Why it matters"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("the readiness report never says %s: %s", want, joined)
		}
	}
	// An optional cell left blank is not a gap.
	if p := IncompleteTableRows(slot, complete); len(p) != 0 {
		t.Errorf("an optional blank was reported as a gap: %v", p)
	}
}
