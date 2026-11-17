package contract

import (
	"encoding/json"
	"fmt"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"os"
	"path/filepath"
	"strings"
)

// ValidateSchema uses the standard JSON Schema validator for every caller.
// Schemas are self-contained: internal references work, external files and URLs
// are refused rather than silently fetching unpinned validation rules.
func compileSchema(schema any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	compiler.UseLoader(schemaResourcesOnly{})
	const resource = "https://hyperplane.invalid/schema.json"
	if err := compiler.AddResource(resource, schema); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

// ReadSchema loads and compiles a self-contained schema before execution.
func ReadSchema(name, schemasDir string) (any, error) {
	if schemasDir == "" {
		schemasDir = "schemas"
	}
	if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
		return nil, fmt.Errorf("schema path must stay under schemas/")
	}
	root, err := os.OpenRoot(schemasDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("cannot read schema %s: %w", name, err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("invalid schema JSON: %w", err)
	}
	if _, err := compileSchema(schema); err != nil {
		return nil, fmt.Errorf("invalid schema %s: %w", name, err)
	}
	return schema, nil
}

func ValidateSchema(value, schema any, at string) string {
	compiled, err := compileSchema(schema)
	if err != nil {
		return fmt.Sprintf("%s: invalid schema: %v", at, err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Sprintf("%s: %v", at, err)
	}
	return ""
}

type schemaResourcesOnly struct{}

func (schemaResourcesOnly) Load(_ string) (any, error) {
	return nil, fmt.Errorf("external schema references are disabled; use a self-contained schema")
}
