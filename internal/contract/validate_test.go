package contract_test

import (
	"encoding/json"
	"github.com/xdanny/hyperplane-workflows/internal/contract"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaEnforcesRulesBeyondTheOldSubset(t *testing.T) {
	for _, tc := range []struct{ schema, value string }{
		{`false`, `{}`},
		{`{"const":"approved"}`, `"rejected"`},
		{`{"type":"string","pattern":"^[A-Z]+$"}`, `"lowercase"`},
		{`{"type":"array","maxItems":1}`, `[1,2]`},
		{`{"anyOf":[{"type":"integer"},{"type":"boolean"}]}`, `"neither"`},
	} {
		var schema, value any
		json.Unmarshal([]byte(tc.schema), &schema)
		json.Unmarshal([]byte(tc.value), &value)
		if reason := contract.ValidateSchema(value, schema, "$"); reason == "" {
			t.Errorf("ignored schema %s for %s", tc.schema, tc.value)
		}
	}
}

func TestSchemaReferenceCannotReadExternalResources(t *testing.T) {
	var schema any
	json.Unmarshal([]byte(`{"$ref":"file:///etc/passwd"}`), &schema)
	if reason := contract.ValidateSchema(nil, schema, "$"); !strings.Contains(reason, "external schema references are disabled") {
		t.Fatalf("external reference: %q", reason)
	}
}

func TestReadSchemaKeepsLocalAndReferenceBoundaries(t *testing.T) {
	root := t.TempDir()
	schemaText := `{"$defs":{"answer":{"type":"integer"}},"type":"object","required":["answer"],"properties":{"answer":{"$ref":"#/$defs/answer"}}}`
	if err := os.WriteFile(filepath.Join(root, "answer.json"), []byte(schemaText), 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := contract.ReadSchema("answer.json", root)
	if err != nil {
		t.Fatal(err)
	}
	if reason := contract.ValidateSchema(map[string]any{"answer": 42}, schema, "output"); reason != "" {
		t.Fatal(reason)
	}
	if reason := contract.ValidateSchema(map[string]any{"answer": "wrong"}, schema, "output"); reason == "" {
		t.Fatal("wrong output type passed")
	}
	for _, name := range []string{"../answer.json", filepath.Join(root, "answer.json"), `sub\answer.json`} {
		if _, err := contract.ReadSchema(name, root); err == nil {
			t.Fatalf("accepted unsafe schema path %q", name)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`true`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ReadSchema("escape.json", root); err == nil {
		t.Fatal("read schema through escaping symlink")
	}
}
