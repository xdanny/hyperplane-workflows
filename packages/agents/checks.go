// Package agentchecks exposes the SDK's static check catalog to the backend.
// The embedded JSON is the same source read by the Node runtime.
package agentchecks

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

//go:embed checks.json
var catalogJSON []byte

type InputSpec struct {
	Required []string `json:"required"`
	Optional []string `json:"optional,omitempty"`
}

// InputTable is advisory form metadata; runtime checks still validate actual inputs.
type InputTable struct {
	Label    string        `json:"label"`
	MinRows  int           `json:"minRows"`
	MaxRows  int           `json:"maxRows"`
	UniqueBy string        `json:"uniqueBy"`
	Columns  []InputColumn `json:"columns"`
}
type InputColumn struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Hint     string   `json:"hint,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
	Format   string   `json:"format,omitempty"`
	Default  string   `json:"default,omitempty"`
}

// Required metadata fields and explicit nulls must agree with the SDK loader.
func decodeMetadataObject(raw []byte, target any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("invalid check input metadata")
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("missing check input metadata %s", key)
		}
	}
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("null check input metadata %s", key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func (table *InputTable) UnmarshalJSON(raw []byte) error {
	type plain InputTable
	return decodeMetadataObject(raw, (*plain)(table), "label", "minRows", "maxRows", "uniqueBy", "columns")
}
func (column *InputColumn) UnmarshalJSON(raw []byte) error {
	type plain InputColumn
	if err := decodeMetadataObject(raw, (*plain)(column), "id", "label"); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, key := range []string{"format", "default"} {
		if value, present := fields[key]; present && bytes.Equal(bytes.TrimSpace(value), []byte(`""`)) {
			return fmt.Errorf("empty check input metadata %s", key)
		}
	}
	return nil
}

type CheckDefinition struct {
	InputTables map[string]InputTable `json:"inputTables,omitempty"`
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	// Summary is the short intro a reader meets first. Description remains the
	// prescriptive methodology the SDK hands the agent, unchanged.
	Summary     string `json:"summary"`
	Description string `json:"description"`
	// The check contract, split so the boundary is findable rather than buried
	// mid-paragraph (D-06): the files the step must produce, what the host
	// measures or verifies itself, what a pass establishes, and what it does not.
	Produces      []string   `json:"produces"`
	Recomputes    []string   `json:"recomputes"`
	Proves        []string   `json:"proves"`
	DoesNotProve  []string   `json:"doesNotProve"`
	Inputs        *InputSpec `json:"inputs,omitempty"`
	Requires      []string   `json:"requires,omitempty"`
	ApprovalAfter string     `json:"approvalAfter,omitempty"`
}

// contractList holds the bound that keeps a contract list readable: short
// sentences, never an empty list, and never a paragraph in disguise.
func contractList(items []string) bool {
	if len(items) < 1 || len(items) > 8 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item) == "" || len(item) > 200 {
			return false
		}
	}
	return true
}

var definitions = func() []CheckDefinition {
	checks, err := decodeCatalog(catalogJSON)
	if err != nil {
		panic(err)
	}
	return checks
}()

func decodeCatalog(raw []byte) ([]CheckDefinition, error) {
	var catalog struct {
		Version int               `json:"version"`
		Checks  []CheckDefinition `json:"checks"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("invalid embedded SDK check catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF || catalog.Version != 1 || len(catalog.Checks) == 0 {
		return nil, fmt.Errorf("invalid embedded SDK check catalog")
	}
	for _, check := range catalog.Checks {
		if strings.TrimSpace(check.Summary) == "" || len(check.Summary) > 900 || strings.ContainsAny(check.Summary, "\r\n") {
			return nil, fmt.Errorf("invalid summary for SDK check %s", check.ID)
		}
		for field, items := range map[string][]string{"produces": check.Produces, "recomputes": check.Recomputes, "proves": check.Proves, "doesNotProve": check.DoesNotProve} {
			if !contractList(items) {
				return nil, fmt.Errorf("invalid %s contract for SDK check %s", field, check.ID)
			}
		}
	}
	name := regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	var shapes struct {
		Checks []map[string]json.RawMessage `json:"checks"`
	}
	_ = json.Unmarshal(raw, &shapes)
	for i, check := range catalog.Checks {
		if _, present := shapes.Checks[i]["inputTables"]; present && len(check.InputTables) == 0 {
			return nil, fmt.Errorf("empty check input tables")
		}
		inputs := map[string]bool{}
		if check.Inputs != nil {
			for _, key := range append(append([]string{}, check.Inputs.Required...), check.Inputs.Optional...) {
				inputs[key] = true
			}
		}
		for key, table := range check.InputTables {
			invalid := !inputs[key] || strings.TrimSpace(table.Label) == "" || table.MinRows < 0 || table.MaxRows < 1 || table.MaxRows < table.MinRows || table.MaxRows > 64 || len(table.Columns) == 0 || len(table.Columns) > 32
			ids := map[string]bool{}
			for _, column := range table.Columns {
				if !name.MatchString(column.ID) || ids[column.ID] || strings.TrimSpace(column.Label) == "" || (column.Format != "" && column.Format != "decimal" && column.Format != "nonnegative-number") {
					invalid = true
				}
				ids[column.ID] = true
				if column.Options != nil && len(column.Options) == 0 {
					invalid = true
				}
				options := map[string]bool{}
				for _, option := range column.Options {
					if option == "" || options[option] {
						invalid = true
					}
					options[option] = true
				}
				if column.Default != "" && column.Options != nil && !options[column.Default] {
					invalid = true
				}
			}
			if invalid || !ids[table.UniqueBy] {
				return nil, fmt.Errorf("invalid input table %s for SDK check %s", key, check.ID)
			}
		}
	}
	return catalog.Checks, nil
}

func clone(d CheckDefinition) CheckDefinition {
	if d.InputTables != nil {
		tables := make(map[string]InputTable, len(d.InputTables))
		for key, table := range d.InputTables {
			table.Columns = append([]InputColumn(nil), table.Columns...)
			for i := range table.Columns {
				table.Columns[i].Options = append([]string(nil), table.Columns[i].Options...)
			}
			tables[key] = table
		}
		d.InputTables = tables
	}
	d.Requires = append([]string(nil), d.Requires...)
	d.Produces = append([]string(nil), d.Produces...)
	d.Recomputes = append([]string(nil), d.Recomputes...)
	d.Proves = append([]string(nil), d.Proves...)
	d.DoesNotProve = append([]string(nil), d.DoesNotProve...)
	if d.Inputs != nil {
		d.Inputs = &InputSpec{
			Required: append([]string(nil), d.Inputs.Required...),
			Optional: append([]string(nil), d.Inputs.Optional...),
		}
	}
	return d
}

// List returns independent copies so callers cannot change runtime requirements.
func List() []CheckDefinition {
	out := make([]CheckDefinition, len(definitions))
	for i, d := range definitions {
		out[i] = clone(d)
	}
	return out
}

func Find(id string) (CheckDefinition, bool) {
	for _, d := range definitions {
		if d.ID == id {
			return clone(d), true
		}
	}
	return CheckDefinition{}, false
}

// Digest binds the backend's displayed catalog to the installed SDK catalog.
func Digest() string { return fmt.Sprintf("%x", sha256.Sum256(catalogJSON)) }
