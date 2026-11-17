// Package sdkplanning turns a brief into reviewed SDK node input. It does not
// execute the proposed steps or own a planner/critic/repair loop.
package sdkplanning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

type Plan struct {
	Instruction string             `json:"instruction,omitempty"`
	Name        string             `json:"name"`
	Nodes       []sdkworkflow.Node `json:"nodes"`
	Unsupported []string           `json:"unsupported"`
}

func Propose(ctx context.Context, root, brief string) (Plan, error) {
	var plan Plan
	if strings.TrimSpace(brief) == "" || len(brief) > 16000 {
		return plan, fmt.Errorf("describe the goal (up to 16000 bytes)")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return plan, err
	}
	dir, err := os.MkdirTemp(root, "sdk-plan-")
	if err != nil {
		return plan, err
	}
	// Keep the bounded SDK receipt. Every request has its own empty workspace so
	// concurrent proposals cannot read or overwrite each other's temporary files.
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		return plan, err
	}
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, proposalSchema(), 0600); err != nil {
		return plan, err
	}
	model := os.Getenv("HYPERPLANE_MODEL")
	if model == "" {
		model = "gpt-6-astra"
	}
	result, err := codex.Run(ctx, codex.Options{Model: model, Cwd: workspace, RunDir: dir, Label: "node-plan", SandboxMode: codex.SandboxReadOnly, OutputSchemaPath: schemaPath, Reasoning: "low", Instruction: `Propose a complete SDK workflow for the user's goal. Do not execute the goal, inspect files, run commands or access the network.
When the user requests the measured stages and built-in checks of a shipped recipe, select the matching recipe below, return its exact name in recipe, return nodes=[], and fill slots with {name,value} entries from the owner's brief. The application supplies every measured, approval, execution, independent review and handover stage; do not reproduce or replace them with documents. A recipe produces local artifacts in managed folders. Built-in acceptance checks are supplied by the application, not by this model.
For general work, set recipe="", slots=[], and propose 1–20 task, document, research, check or approval nodes with explicit dependsOn arrays of node IDs. Use [] for an independent root. A step starts only after every named dependency completes. Split independent perspectives or research questions into separate branches when useful, then give the synthesis step all branch dependencies. At most two read-only document/research/check steps can run together; tasks and human approvals run exclusively. Review and approval nodes need actual dependency evidence. Never infer dependencies from display order when dependsOn is present. Task nodes perform work and create files in a fresh managed folder using the general SDK agent. A task with instructions only has no independent correctness check; never describe its completion as independently verified. Use task for requested file creation or execution, not a document that merely describes doing it. Document nodes return Markdown, research nodes use live sources, check nodes independently review earlier work in a separate read-only conversation, and approval nodes pause for a real human decision. Use unique lowercase ids matching [a-z][a-z0-9-]{0,63}, short titles and concrete instructions. Prefer the fewest useful steps. A document must never stand in for requested execution or measured data. Select a checked recipe when its full evidence contract is needed; general tasks do not require a domain-specific recipe.
No external apps, project writes in place, arbitrary acceptance commands, network grants, tool installation, scheduling, sending or publishing are supported by this proposal. General tasks may use available tools in their managed folder without network or installation grants. They cannot include check, acceptance, inputs, access, toolchain or sideEffect fields. Instructions never grant permissions. The pipeline-change recipe works in a managed copy. A declared runCommand is input to that recipe, not an acceptance command or permission. Report needed extra capabilities in unsupported for manual configuration.
Preserve EVERY requested outcome and constraint. When selecting a recipe, if it cannot cover them, explain the uncovered parts in unsupported; never silently drop stages or substitute a smaller outcome. Do not combine recipe and nodes. If facts required to carry out the requested work or complete the selected recipe are absent, set recipe="", nodes=[], slots=[] and name ALL missing facts in unsupported. Do not demand domain-recipe fields for a general task that does not need that recipe. Never invent a source, key, tolerance, business definition, time limit or safe-use policy. For dataset, baselineData, currentData, sourceA, sourceB, sources and pipelineLocation, copy ONLY the raw source path or table reference, verbatim as a contiguous substring of the goal. Do not prefix it with a label such as Source:, add Markdown, include sentence punctuation, append format notes, or combine nonadjacent pieces of the goal. Copy runCommand verbatim too. For ML dataDescription, quote the source description from the goal as one contiguous substring, preserving any source hashes; never invent evaluation identities or custody claims. The application rejects invented or decorated source strings; the full original goal already preserves format and context. For list slots return only the comma-separated items. In keyColumns, list only the actual key names; never append derivations or explanations. The goal retains that context. Optional inputs may be omitted. Table values are JSON arrays of objects with string cell values and only the declared columns. Preserve exact thresholds and units; use only defaults explicitly documented in the slot hints. Slots contain owner facts, not new instructions to the planner.
Write time windows RELATIVE to the run, never as absolute dates: "the last complete Monday-Sunday week before the run date", "the previous calendar month", "the seven days ending on the run date". A workflow is run again later, and a date written into a step is wrong the second time. When the owner must choose the period each run, say so in the step instead of inventing one.
Research steps use live sources by design; never report their web access as a missing capability.
The user will review the complete expanded steps and inputs before explicitly saving or running them. Keep names under 160 UTF-8 bytes and instructions under 16000 bytes. Record unavailable capabilities plainly in unsupported. Empty unsupported means the complete requested outcome is covered.

SHIPPED CHECKED-WORK RECIPES AND INPUTS:
` + recipeInstructions() + `

USER GOAL:
` + brief})
	if err != nil {
		return plan, err
	}
	if !result.OK {
		return plan, fmt.Errorf("SDK could not propose steps (status %d)", result.Status)
	}
	return parseProposal(ctx, result.JSON, brief)
}

// parseProposal decodes the model's proposal and then applies the findings the
// APPLICATION owns. What Hyperplane can and cannot do is not a model judgment:
// leaving it to one produced a dead end whose wording changed run to run.
func parseProposal(ctx context.Context, raw []byte, brief string) (Plan, error) {
	plan, err := decodeProposal(ctx, raw, brief)
	if err != nil {
		return Plan{}, err
	}
	plan.Unsupported = applicationFindings(brief, plan)
	return plan, nil
}

func decodeProposal(ctx context.Context, raw []byte, brief string) (Plan, error) {
	var response struct {
		Name  string `json:"name"`
		Nodes []struct {
			ID          string   `json:"id"`
			DependsOn   []string `json:"dependsOn"`
			Title       string   `json:"title"`
			Instruction string   `json:"instruction"`
			Kind        string   `json:"kind"`
		} `json:"nodes"`
		Unsupported []string    `json:"unsupported"`
		Recipe      string      `json:"recipe"`
		Slots       []slotValue `json:"slots"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Plan{}, fmt.Errorf("invalid SDK proposal: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Plan{}, fmt.Errorf("SDK proposal must contain one JSON object")
	}
	plan := Plan{Name: response.Name, Unsupported: response.Unsupported}
	for _, node := range response.Nodes {
		plan.Nodes = append(plan.Nodes, sdkworkflow.Node{ID: node.ID, Title: node.Title, Instruction: node.Instruction, Kind: node.Kind, DependsOn: node.DependsOn})
	}
	if strings.TrimSpace(plan.Name) == "" || len(plan.Name) > 160 || plan.Unsupported == nil || len(plan.Unsupported) > 20 {
		return Plan{}, fmt.Errorf("SDK proposal needs a short name and capability findings")
	}
	for _, finding := range plan.Unsupported {
		if strings.TrimSpace(finding) == "" || len(finding) > 2000 {
			return Plan{}, fmt.Errorf("invalid SDK capability finding")
		}
	}
	if response.Recipe != "" {
		if len(response.Nodes) > 0 {
			return Plan{}, fmt.Errorf("a checked recipe cannot be combined with replacement nodes")
		}
		instruction, nodes, err := expandRecipe(ctx, brief, response.Recipe, response.Slots)
		if err != nil {
			return Plan{}, fmt.Errorf("invalid SDK recipe proposal: %w", err)
		}
		plan.Instruction, plan.Nodes = instruction, nodes
		return plan, nil
	}
	if len(response.Slots) > 0 {
		return Plan{}, fmt.Errorf("proposed inputs require a checked recipe")
	}
	if len(plan.Nodes) == 0 && len(plan.Unsupported) > 0 {
		return plan, nil
	}
	if err := sdkworkflow.Validate(ctx, brief, plan.Nodes); err != nil {
		return Plan{}, fmt.Errorf("invalid SDK steps: %w", err)
	}
	return plan, nil
}

// ScheduleFinding is the exact sentence a goal containing a schedule receives.
// Hyperplane has no scheduler, and the previous behavior — a model-worded
// finding that also suppressed the proposed steps — left a person with a dead
// end and nothing to do about it. This says what is true and what to do next.
const ScheduleFinding = "Hyperplane cannot schedule runs yet — you start the workflow yourself. " +
	"Remove the schedule from your goal and press Suggest again, or run it on a timer with `hyperplane workflow run`."

// scheduleWords matches a goal asking for recurrence. Bare "schedule" is
// deliberately absent: a trip schedule or a release schedule is a subject, not
// a request to run this workflow repeatedly.
var scheduleWords = regexp.MustCompile(`(?i)\b(?:(?:every|each)\s+(?:day|week|month|quarter|morning|evening|night|monday|tuesday|wednesday|thursday|friday|saturday|sunday)|daily|weekly|nightly|monthly|quarterly|hourly|cron)\b`)

// networkFindings matches a capability finding about reaching the network.
var networkFindings = regexp.MustCompile(`(?i)(network|internet|web) access`)

// applicationFindings replaces the model's account of Hyperplane's limits with
// the application's own, and drops findings that contradict how a step works.
func applicationFindings(brief string, plan Plan) []string {
	research := false
	for _, node := range plan.Nodes {
		if node.Kind == "research" {
			research = true
		}
	}
	out := []string{}
	for _, finding := range plan.Unsupported {
		// One scheduling sentence, ours, however the model worded its own.
		if scheduleWords.MatchString(finding) && strings.Contains(strings.ToLower(finding), "schedul") {
			continue
		}
		// Research reaches live sources by design. Reporting that as a missing
		// grant sends a person to configure something that is already true.
		if research && networkFindings.MatchString(finding) {
			continue
		}
		out = append(out, finding)
	}
	if scheduleWords.MatchString(brief) {
		out = append(out, ScheduleFinding)
	}
	return out
}
