// Package sdkworkflow defines the saved input to the shared SDK extension.
// Semantic validation and execution rules belong to packages/agents.
package sdkworkflow

import (
	"context"
	"encoding/json"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

type Node struct {
	DependsOn   []string          `json:"dependsOn,omitempty"`
	Access      *TaskAccess       `json:"access,omitempty"`
	Toolchain   *bool             `json:"toolchain,omitempty"`
	SideEffect  *bool             `json:"sideEffect,omitempty"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	Acceptance  string            `json:"acceptance,omitempty"`
	Check       []string          `json:"check,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Instruction string            `json:"instruction"`
}

// Access is caller-owned input. Proposals cannot grant execution permissions.
type TaskAccess struct {
	Hosts       []string `json:"hosts"`
	ReadPaths   []string `json:"readPaths,omitempty"`
	Environment []string `json:"environment,omitempty"`
}

type ValidationReport = agentchecks.ValidationReport
type ValidationIssue = agentchecks.ValidationIssue

var ErrValidationUnavailable = agentchecks.ErrValidationUnavailable

// MarshalJSON preserves explicit empty dependencies, commands and input maps.
// Empty dependencies mean an independent root; nil (including JSON null) retains
// the legacy previous-step dependency.
func (n Node) MarshalJSON() ([]byte, error) {
	type plain Node
	raw, err := json.Marshal(plain(n))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if n.Check != nil {
		fields["check"], err = json.Marshal(n.Check)
		if err != nil {
			return nil, err
		}
	}
	if n.Inputs != nil {
		fields["inputs"], err = json.Marshal(n.Inputs)
		if err != nil {
			return nil, err
		}
	}
	if n.DependsOn != nil {
		fields["dependsOn"], err = json.Marshal(n.DependsOn)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(fields)
}

// Inspect returns SDK validation findings and the revision that produced them.
func Inspect(ctx context.Context, instruction string, nodes []Node) (ValidationReport, error) {
	raw, err := json.Marshal(nodes)
	if err != nil {
		return ValidationReport{}, err
	}
	return agentchecks.InspectWorkflow(ctx, instruction, raw)
}
func Validate(ctx context.Context, instruction string, nodes []Node) error {
	report, err := Inspect(ctx, instruction, nodes)
	if err != nil {
		return err
	}
	if !report.Valid {
		return report.Issue
	}
	return nil
}
