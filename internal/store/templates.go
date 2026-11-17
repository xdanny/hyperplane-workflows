package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

// WorkflowTemplate is a reusable workflow scaffold — a recipe when it declares
// context slots and criteria. ContextSlots say what must be known before it can
// start; Criteria say what it must prove before it is done; Spec is the fixed
// orchestration that turns one into the other.
type WorkflowTemplate struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Spec         JSON          `json:"spec"`
	ContextSlots []ContextSlot `json:"contextSlots"`
	Criteria     []Criterion   `json:"criteria"`
	CreatedAt    time.Time     `json:"createdAt"`

	// Display-only fields derived when templates are served.
	DisplayName string         `json:"displayName,omitempty"`
	Outputs     []RecipeOutput `json:"outputs,omitempty"`
}

// RecipeOutput describes an expected SDK step result for the template form.
type RecipeOutput struct {
	Name     string `json:"name"`
	ForOwner string `json:"forOwner"`
}

// IsRecipe reports whether this template asks for context up front.
func (t WorkflowTemplate) IsRecipe() bool { return len(t.ContextSlots) > 0 }

func scanTemplate(row pgx.Row) (WorkflowTemplate, error) {
	var t WorkflowTemplate
	var desc *string
	err := row.Scan(&t.ID, &t.Name, &desc, &t.Spec, &t.ContextSlots, &t.Criteria, &t.CreatedAt)
	if err != nil {
		return t, err
	}
	t.Description = deref(desc)
	// Never hand callers a null array — the UI and the CLI both iterate these.
	if t.ContextSlots == nil {
		t.ContextSlots = []ContextSlot{}
	}
	if t.Criteria == nil {
		t.Criteria = []Criterion{}
	}
	return t, nil
}

const templateCols = `id, name, description, spec, context_slots, criteria, created_at`

// ListTemplates returns all workflow templates ordered by name.
func (s *Store) ListTemplates(ctx context.Context) ([]WorkflowTemplate, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+templateCols+` FROM workflow_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()
	var out []WorkflowTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate fetches one workflow template by id.
func (s *Store) GetTemplate(ctx context.Context, id string) (WorkflowTemplate, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+templateCols+` FROM workflow_templates WHERE id=$1`, id)
	t, err := scanTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// CreateTemplate inserts a new workflow template and returns the stored record.
func (s *Store) CreateTemplate(ctx context.Context, t WorkflowTemplate) (WorkflowTemplate, error) {
	if len(t.Spec) == 0 {
		t.Spec = JSON("{}")
	}
	if t.ContextSlots == nil {
		t.ContextSlots = []ContextSlot{}
	}
	if t.Criteria == nil {
		t.Criteria = []Criterion{}
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO workflow_templates (name, description, spec, context_slots, criteria)
		VALUES ($1, NULLIF($2,''), $3, $4, $5)
		RETURNING `+templateCols,
		t.Name, t.Description, t.Spec, t.ContextSlots, t.Criteria,
	)
	out, err := scanTemplate(row)
	if err != nil {
		return out, fmt.Errorf("create template: %w", err)
	}
	return out, nil
}

// RefreshTemplateByName updates the recipe content (description, spec, context
// slots, criteria) of the template with the given name. Used by seeding to keep
// shipped recipes current on existing installs; the id (and any runs stamped
// with it) stays stable. Returns ErrNotFound when no template has that name.
func (s *Store) RefreshTemplateByName(ctx context.Context, t WorkflowTemplate) error {
	if t.ContextSlots == nil {
		t.ContextSlots = []ContextSlot{}
	}
	if t.Criteria == nil {
		t.Criteria = []Criterion{}
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE workflow_templates
		SET description = NULLIF($2,''), spec = $3, context_slots = $4, criteria = $5
		WHERE name = $1`,
		t.Name, t.Description, t.Spec, t.ContextSlots, t.Criteria,
	)
	if err != nil {
		return fmt.Errorf("refresh template %q: %w", t.Name, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InstantiateResult contains an unsaved SDK workflow and the reviewed template inputs.
type InstantiateResult struct {
	Workflow *SDKWorkflow
	Template WorkflowTemplate
	// Values are the accepted slot values (declared + non-blank only).
	Values map[string]string
}

// InstantiateTemplate prepares an SDK workflow without saving or starting it.
// Required slots remain mandatory; no rehearsal values or graph draft is created.
//
// fill says whether the accepted values are SUBSTITUTED INTO the prepared
// instructions. Preparing for review keeps `{slot:x}` tokens and carries the
// template's slots as the workflow's own inputs, so saving the result yields a
// workflow that can run again next week with new values instead of prose that
// has this week's answers baked into it. Launching directly (run:true) fills
// them, because a run needs values, not placeholders.
func (s *Store) InstantiateTemplate(ctx context.Context, id string, values map[string]string, project string, fill bool) (*InstantiateResult, error) {
	t, err := s.GetTemplate(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("instantiate template: %w", err)
	}
	if missing := MissingSlots(t.ContextSlots, values); len(missing) > 0 {
		return nil, &MissingSlotsError{Missing: missing}
	}

	accepted := FilterSlotValues(t.ContextSlots, values)
	// A run may tighten a tolerance the project stands behind; it may never
	// widen one. Checked here because this is the last moment where the run's
	// numbers and the project's standing numbers are both in hand and nothing
	// has been measured against either.
	if err := s.RefuseLoosenedTolerances(ctx, project, t.ContextSlots, accepted); err != nil {
		return nil, err
	}
	spec := t.Spec
	if fill {
		var err error
		spec, err = FillSlots(t.Spec, t.ContextSlots, accepted)
		if err != nil {
			return nil, fmt.Errorf("instantiate template %s: %w", id, err)
		}
	}
	var shape struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(spec, &shape); err != nil {
		return nil, err
	}
	if shape.Kind != "nodes" {
		return nil, fmt.Errorf("%w: templates must contain an SDK node workflow", ErrInvalidWorkflow)
	}
	var workflow SDKWorkflow
	decoder := json.NewDecoder(bytes.NewReader(spec))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&workflow); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWorkflow, err)
	}
	if err := sdkworkflow.Validate(ctx, workflow.Instruction, workflow.Nodes); err != nil {
		return nil, workflowValidationError(err)
	}
	if workflow.ID != "" || workflow.TargetRoot != "" || len(workflow.Check) > 0 {
		return nil, ErrInvalidWorkflow
	}
	workflow.Project, workflow.Name, workflow.Check = project, t.Name, []string{}
	// The template's slots become the prepared workflow's own inputs. Saving it
	// keeps the questions; filling them stays a per-run act.
	workflow.Inputs = t.ContextSlots
	if workflow.Inputs == nil {
		workflow.Inputs = []ContextSlot{}
	}
	return &InstantiateResult{Template: t, Workflow: &workflow, Values: accepted}, nil
}
