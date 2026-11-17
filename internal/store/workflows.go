package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

// SDKWorkflow saves reviewed launch settings and optional ordered SDK steps.
type SDKWorkflow struct {
	Nodes []sdkworkflow.Node `json:"nodes,omitempty"`
	// Inputs are the values a person supplies EACH RUN, declared once here and
	// referenced as `{slot:<id>}` tokens in the goal, step instructions, step
	// source inputs and readable-input paths. Always present (empty when the
	// workflow asks for nothing), because every reader iterates it.
	Inputs      []ContextSlot `json:"inputs"`
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Instruction string        `json:"instruction"`
	TargetRoot  string        `json:"targetRoot"`
	Check       []string      `json:"check"`
	Project     string        `json:"project"`
	// SignOff is the workflow's sign-off policy: "required" (a person decides
	// every finished run), "optional" (a finished run stays decidable but never
	// queues) or "auto_when_checks_pass" (the server records an automatic
	// acceptance when every declared check of a passed run passed). Empty on the
	// wire means "required"; the stored value is always one of the three.
	SignOff    string    `json:"signOff"`
	AutoRepair bool      `json:"autoRepair"`
	Revision   int64     `json:"revision"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

var ErrWorkflowChanged = errors.New("workflow changed or was removed; reload it before saving or removing")
var ErrInvalidWorkflow = errors.New("invalid workflow")

const sdkWorkflowColumns = `id,name,kind,instruction,target_root,check_argv,project,auto_repair,revision,updated_at,nodes,inputs,sign_off`

// Sign-off policies. SignOffRequired is what every workflow did before the
// policy existed, so it is both the column default and the empty-value meaning.
const (
	SignOffRequired     = "required"
	SignOffOptional     = "optional"
	SignOffAutoOnChecks = "auto_when_checks_pass"
)

// ValidSignOff reports whether a policy is one this build understands. An
// unknown value is a client mistake, not a new behavior to guess at.
func ValidSignOff(policy string) bool {
	switch policy {
	case SignOffRequired, SignOffOptional, SignOffAutoOnChecks:
		return true
	}
	return false
}

func scanSDKWorkflow(row pgx.Row) (SDKWorkflow, error) {
	var w SDKWorkflow
	var nodes []byte
	err := row.Scan(&w.ID, &w.Name, &w.Kind, &w.Instruction, &w.TargetRoot, &w.Check, &w.Project, &w.AutoRepair, &w.Revision, &w.UpdatedAt, &nodes, &w.Inputs, &w.SignOff)
	if err == nil {
		err = json.Unmarshal(nodes, &w.Nodes)
	}
	if w.Inputs == nil {
		w.Inputs = []ContextSlot{}
	}
	if w.Kind == "task" && len(w.Check) == 0 {
		w.Check = nil
	}
	return w, err
}

func (s *Store) ListSDKWorkflows(ctx context.Context) ([]SDKWorkflow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+sdkWorkflowColumns+` FROM sdk_workflows ORDER BY lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SDKWorkflow{}
	for rows.Next() {
		w, err := scanSDKWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetSDKWorkflow reads one saved workflow. Launching from a saved definition
// reads it here so the run uses the stored steps, not a client's copy of them.
func (s *Store) GetSDKWorkflow(ctx context.Context, id string) (SDKWorkflow, error) {
	w, err := scanSDKWorkflow(s.Pool.QueryRow(ctx, `SELECT `+sdkWorkflowColumns+` FROM sdk_workflows WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

func (s *Store) SaveSDKWorkflow(ctx context.Context, w SDKWorkflow) (SDKWorkflow, error) {
	w.Name, w.Instruction = strings.TrimSpace(w.Name), strings.TrimSpace(w.Instruction)
	if w.Name == "" || len(w.Name) > 160 || w.Instruction == "" || len(w.Instruction) > 16000 {
		return w, fmt.Errorf("%w: provide a name (up to 160 bytes) and goal (up to 16000 bytes)", ErrInvalidWorkflow)
	}
	if w.Kind != "nodes" && len(w.Nodes) != 0 {
		return w, ErrInvalidWorkflow
	}
	if w.SignOff == "" {
		w.SignOff = SignOffRequired
	}
	if !ValidSignOff(w.SignOff) {
		return w, fmt.Errorf("%w: signOff must be %q, %q or %q", ErrInvalidWorkflow, SignOffRequired, SignOffOptional, SignOffAutoOnChecks)
	}
	if err := ValidateWorkflowInputs(w); err != nil {
		return w, err
	}
	switch w.Kind {
	case "nodes":
		if w.TargetRoot != "" || len(w.Check) != 0 {
			return w, ErrInvalidWorkflow
		}
		if err := sdkworkflow.Validate(ctx, w.Instruction, w.Nodes); err != nil {
			return w, workflowValidationError(err)
		}
	case "task":
		if !filepath.IsAbs(w.TargetRoot) {
			return w, fmt.Errorf("%w: project tasks need an absolute working folder", ErrInvalidWorkflow)
		}
		if err := sdkworkflow.Validate(ctx, w.Instruction, []sdkworkflow.Node{{ID: "task", Kind: "task", Title: "Project task", Instruction: w.Instruction, Check: w.Check}}); err != nil {
			return w, workflowValidationError(err)
		}
	case "app-ideator":
		if w.TargetRoot != "" || len(w.Check) != 0 || len(w.Instruction) > 4000 {
			return w, fmt.Errorf("%w: app ideas use a built-in workspace and check, and a goal up to 4000 bytes", ErrInvalidWorkflow)
		}
	default:
		return w, fmt.Errorf("%w: choose a node workflow, project task or app ideas", ErrInvalidWorkflow)
	}
	if w.Check == nil {
		w.Check = []string{} // SQL stores omitted optional checks as an empty array.
	}
	if w.Nodes == nil {
		w.Nodes = []sdkworkflow.Node{}
	}
	if w.Inputs == nil {
		w.Inputs = []ContextSlot{}
	}
	nodes, err := json.Marshal(w.Nodes)
	if err != nil {
		return w, err
	}
	inputs, err := json.Marshal(w.Inputs)
	if err != nil {
		return w, err
	}
	if w.ID == "" {
		if w.Revision != 0 {
			return w, ErrInvalidWorkflow
		}
		return scanSDKWorkflow(s.Pool.QueryRow(ctx, `INSERT INTO sdk_workflows(name,kind,instruction,target_root,check_argv,project,auto_repair,nodes,inputs,sign_off)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+sdkWorkflowColumns,
			w.Name, w.Kind, w.Instruction, w.TargetRoot, w.Check, w.Project, w.AutoRepair, nodes, inputs, w.SignOff))
	}
	updated, err := scanSDKWorkflow(s.Pool.QueryRow(ctx, `UPDATE sdk_workflows SET name=$1,kind=$2,instruction=$3,target_root=$4,check_argv=$5,project=$6,auto_repair=$7,revision=revision+1,updated_at=now(),nodes=$10,inputs=$11,sign_off=$12
	 WHERE id=$8 AND revision=$9 RETURNING `+sdkWorkflowColumns,
		w.Name, w.Kind, w.Instruction, w.TargetRoot, w.Check, w.Project, w.AutoRepair, w.ID, w.Revision, nodes, inputs, w.SignOff))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrWorkflowChanged
	}
	return updated, err
}

func (s *Store) DeleteSDKWorkflow(ctx context.Context, id string, revision int64) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM sdk_workflows WHERE id=$1 AND revision=$2`, id, revision)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrWorkflowChanged
	}
	return nil
}

// Validation infrastructure failure is not malformed user input.
func workflowValidationError(err error) error {
	var issue *sdkworkflow.ValidationIssue
	if errors.As(err, &issue) {
		return fmt.Errorf("%w: %w", ErrInvalidWorkflow, err)
	}
	return err
}
