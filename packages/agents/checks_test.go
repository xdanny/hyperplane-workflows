package agentchecks

import (
	"strings"
	"testing"
)

func TestCatalogReadersCannotChangeOtherCallersRequirements(t *testing.T) {
	all := List()
	profile, _ := Find("data-quality-profile")
	original := profile.Inputs.Required[0]
	profile.Inputs.Required[0] = "attacker-source"
	for i := range all {
		if all[i].Inputs != nil {
			all[i].Inputs.Required[0] = "changed"
		}
		if len(all[i].Requires) > 0 {
			all[i].Requires[0] = "changed"
		}
		all[i].Description = "changed"
	}
	again, _ := Find("data-quality-profile")
	if again.Inputs.Required[0] != original || again.Description == "changed" {
		t.Fatal("caller mutated shared catalog")
	}
	audit, _ := Find("data-quality-audit")
	if audit.Requires[0] == "changed" {
		t.Fatal("caller mutated shared prerequisites")
	}
}

func TestInputTablesAreCopiedAndMalformedMetadataIsRejected(t *testing.T) {
	profile, _ := Find("dataset-shift-profile")
	table := profile.InputTables["features"]
	table.Columns[1].Options[0] = "changed"
	profile.InputTables["features"] = InputTable{}
	again, _ := Find("dataset-shift-profile")
	if again.InputTables["features"].Columns[1].Options[0] != "numeric" {
		t.Fatal("caller changed catalog table")
	}
	original := `{"version":1,"checks":[{"id":"example","title":"Example","summary":"A check","description":"A check",` + contract + `,"inputs":{"required":["rows"]},"inputTables":{"rows":{"label":"Rows","minRows":1,"maxRows":32,"uniqueBy":"name","columns":[{"id":"name","label":"Name","required":true}]}}}]}`
	for _, changed := range []string{
		strings.Replace(original, `"maxRows":32`, `"maxRows":65`, 1),
		strings.Replace(original, `"minRows":1,`, "", 1),
		strings.Replace(original, `"uniqueBy":"name"`, `"uniqueBy":"missing"`, 1),
		strings.Replace(original, `"required":true`, `"required":null`, 1),
		strings.Replace(original, `"label":"Name"`, `"label":"Name","format":"money"`, 1),
		strings.Replace(original, `"label":"Name"`, `"label":"Name","options":[]`, 1),
		strings.Replace(original, `"label":"Name"`, `"label":"Name","unknown":true`, 1),
		strings.Replace(original, `"label":"Name"`, `"label":"Name","options":["a"],"default":"b"`, 1),
	} {
		if _, err := decodeCatalog([]byte(changed)); err == nil {
			t.Fatalf("accepted malformed table metadata: %s", changed)
		}
	}
	if _, err := decodeCatalog([]byte(original)); err != nil {
		t.Fatal(err)
	}
}

// contract is the minimal four-list check contract a catalog entry must carry.
const contract = `"produces":["report.json"],"recomputes":["Reads report.json"],"proves":["The file exists"],"doesNotProve":["That the numbers are right"]`

func TestEveryCheckStatesItsContractAndItsBoundary(t *testing.T) {
	for _, check := range List() {
		if strings.TrimSpace(check.Summary) == "" || len(check.Summary) > 900 {
			t.Fatalf("%s has no usable summary", check.ID)
		}
		for field, items := range map[string][]string{"produces": check.Produces, "recomputes": check.Recomputes, "proves": check.Proves, "doesNotProve": check.DoesNotProve} {
			if !contractList(items) {
				t.Fatalf("%s has an unusable %s contract: %v", check.ID, field, items)
			}
		}
		// The boundary is the point of the split: a check that claims a pass
		// proves everything is the failure this replaced.
		if len(check.DoesNotProve) == 0 {
			t.Fatalf("%s states no boundary", check.ID)
		}
	}
	// Copies cannot change what a later reader is told.
	first := List()[0]
	first.DoesNotProve[0] = "changed"
	if List()[0].DoesNotProve[0] == "changed" {
		t.Fatal("caller mutated the shared check contract")
	}
}

func TestMalformedCheckContractsAreRejected(t *testing.T) {
	base := `{"version":1,"checks":[{"id":"example","title":"Example","summary":"A check","description":"A check",` + contract + `}]}`
	if _, err := decodeCatalog([]byte(base)); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 201)
	for name, changed := range map[string]string{
		"missing doesNotProve": strings.Replace(base, `,"doesNotProve":["That the numbers are right"]`, "", 1),
		"empty doesNotProve":   strings.Replace(base, `["That the numbers are right"]`, `[]`, 1),
		"empty produces":       strings.Replace(base, `["report.json"]`, `[]`, 1),
		"blank item":           strings.Replace(base, `["report.json"]`, `["   "]`, 1),
		"oversized item":       strings.Replace(base, `["report.json"]`, `["`+long+`"]`, 1),
		"too many items":       strings.Replace(base, `["report.json"]`, `["a","b","c","d","e","f","g","h","i"]`, 1),
		"missing produces":     strings.Replace(base, `"produces":["report.json"],`, "", 1),
		"missing summary":      strings.Replace(base, `"summary":"A check",`, "", 1),
		"paragraph summary":    strings.Replace(base, `"summary":"A check"`, `"summary":"A check\nwith a second paragraph"`, 1),
	} {
		if _, err := decodeCatalog([]byte(changed)); err == nil {
			t.Fatalf("accepted %s: %s", name, changed)
		}
	}
}
