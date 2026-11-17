package seed_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// recurringTemplates are the five workflows meant to be rerun every week or
// month with new input values. Each one is held to the shape that makes a
// rerun trustworthy: the facts that change are slots, the judgement is an
// independent check with enumerated failure criteria, and a person signs off
// before anything is written for an audience outside the run.
var recurringTemplates = []string{
	"competitor-brief",
	"kpi-commentary",
	"plain-language-pass",
	"customer-feedback-digest",
	"file-landing-audit",
}

func recurringByName(t *testing.T) map[string]store.WorkflowTemplate {
	t.Helper()
	byName := make(map[string]store.WorkflowTemplate, len(seed.DefaultTemplates))
	for _, tmpl := range seed.DefaultTemplates {
		byName[tmpl.Name] = tmpl
	}
	out := make(map[string]store.WorkflowTemplate, len(recurringTemplates))
	for _, name := range recurringTemplates {
		tmpl, ok := byName[name]
		if !ok {
			t.Fatalf("DefaultTemplates: missing recurring template %q — it would not be seeded at all", name)
		}
		out[name] = tmpl
	}
	return out
}

func recurringWorkflow(t *testing.T, tmpl store.WorkflowTemplate) store.SDKWorkflow {
	t.Helper()
	var workflow store.SDKWorkflow
	if err := json.Unmarshal(tmpl.Spec, &workflow); err != nil {
		t.Fatalf("recipe %q: spec is not an SDK definition: %v", tmpl.Name, err)
	}
	if workflow.Kind != "nodes" {
		t.Fatalf("recipe %q: kind is %q, want nodes", tmpl.Name, workflow.Kind)
	}
	return workflow
}

var slotToken = regexp.MustCompile(`\{slot:([a-zA-Z0-9_-]+)\}`)

// TestTemplateTokensNameADeclaredSlot is the other half of the slot contract.
// TestRecipeSlotsAreFillableAndUsed catches a declared slot nothing uses; this
// catches a token no slot declares, which is worse: the form never asks for the
// value, FillSlots leaves the token in place, and the step runs with the literal
// text "{slot:month}" where the month was supposed to be.
func TestTemplateTokensNameADeclaredSlot(t *testing.T) {
	for _, tmpl := range seed.DefaultTemplates {
		declared := make(map[string]bool, len(tmpl.ContextSlots))
		for _, s := range tmpl.ContextSlots {
			declared[s.ID] = true
		}
		for _, m := range slotToken.FindAllStringSubmatch(string(tmpl.Spec), -1) {
			if !declared[m[1]] {
				t.Errorf("recipe %q: the spec substitutes {slot:%s}, which no context slot declares — the run would receive the token itself", tmpl.Name, m[1])
			}
		}
	}
}

// TestRecurringTemplatesAreTitledAndNamedForReuse: the slug is what
// `hyperplane workflow run <name>` takes, so it has to stay a slug. The store
// does not persist DisplayName (it is derived only when templates are served),
// so the human title lives in the first sentence of the description, which is
// what the picker shows.
func TestRecurringTemplatesAreTitledAndNamedForReuse(t *testing.T) {
	slug := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	for name, tmpl := range recurringByName(t) {
		if !slug.MatchString(name) {
			t.Errorf("recipe %q: name is not a stable slug — it is the CLI argument", name)
		}
		title, _, found := strings.Cut(tmpl.Description, " — ")
		if !found || len(title) < 8 {
			t.Errorf("recipe %q: description does not open with a title followed by \" — \", so the picker has only the slug to show:\n%s", name, tmpl.Description)
		}
	}
}

// TestRecurringSlotHintsAreWrittenForTheirReader enforces the ContextSlot.Hint
// contract (internal/store/recipes.go): the hint is read by THE PERSON FILLING
// THE SLOT, not by the model that consumes the value. A hint that does not
// address that person is how a form ends up asking an owner for an engineer's
// answer with no way to tell.
func TestRecurringSlotHintsAreWrittenForTheirReader(t *testing.T) {
	reader := regexp.MustCompile(`(?i)\b(you|your|yourself)\b`)
	for name, tmpl := range recurringByName(t) {
		for _, s := range tmpl.ContextSlots {
			if len(s.Hint) < 80 {
				t.Errorf("recipe %q: slot %q has a %d-character hint — too thin to say why the value matters or where to find it:\n%s", name, s.ID, len(s.Hint), s.Hint)
			}
			if !reader.MatchString(s.Hint) {
				t.Errorf("recipe %q: slot %q's hint never addresses the person filling it, so it reads as a note to the model:\n%s", name, s.ID, s.Hint)
			}
			if strings.Contains(string(tmpl.Spec), s.Hint) {
				t.Errorf("recipe %q: slot %q's hint is copied from the step instructions — the model-facing sentence is never the person-facing one", name, s.ID)
			}
		}
	}
}

// TestRecurringChecksStateConcreteFailureCriteria is the guard on the step that
// makes a rerun worth trusting. "Review the document and report problems" is a
// second opinion about tone; a check earns its place by naming, before the run,
// the conditions under which it fails. The shipped convention is a lettered
// list, so a criterion cannot be quietly merged into a sentence.
func TestRecurringChecksStateConcreteFailureCriteria(t *testing.T) {
	for name, tmpl := range recurringByName(t) {
		workflow := recurringWorkflow(t, tmpl)
		checks := 0
		for _, node := range workflow.Nodes {
			if node.Kind != "check" {
				continue
			}
			checks++
			instruction := node.Instruction
			if !strings.Contains(instruction, "FAIL if any of these is true:") {
				t.Errorf("recipe %q step %q: no enumerated failure criteria — a reviewer with no stated bar passes whatever it is shown:\n%s", name, node.ID, instruction)
			}
			for _, marker := range []string{"(a) ", "(b) ", "(c) ", "(d) "} {
				if !strings.Contains(instruction, marker) {
					t.Errorf("recipe %q step %q: fewer than four lettered failure criteria (missing %q)", name, node.ID, strings.TrimSpace(marker))
				}
			}
			// Every "passed" wording has to keep saying what it did not
			// establish; a check that only says what a pass proves is read as
			// proof of the whole workflow.
			if !strings.Contains(instruction, "does not mean") {
				t.Errorf("recipe %q step %q: never says what a pass does NOT mean, so its verdict reads as general correctness:\n%s", name, node.ID, instruction)
			}
		}
		if checks == 0 {
			t.Errorf("recipe %q: no independent check step", name)
		}
	}
}

// TestRecurringTemplatesStopForAPerson: every one of these produces something
// that would otherwise go to colleagues, customers or an on-call channel.
// A person decides, and the decision comes before the step that writes the
// outgoing text.
func TestRecurringTemplatesStopForAPerson(t *testing.T) {
	for name, tmpl := range recurringByName(t) {
		workflow := recurringWorkflow(t, tmpl)
		approvals, approvalIndex := 0, -1
		for i, node := range workflow.Nodes {
			if node.Kind == "approval" {
				approvals++
				if approvalIndex < 0 {
					approvalIndex = i
				}
			}
		}
		if approvals == 0 {
			t.Errorf("recipe %q: no approval step — a weekly workflow with no human decision publishes itself", name)
			continue
		}
		if approvalIndex == 0 {
			t.Errorf("recipe %q: the approval is the first step, so there is nothing to approve", name)
		}
		if approvalIndex == len(workflow.Nodes)-1 {
			continue // The approval is the last word; nothing follows it.
		}
		for _, node := range workflow.Nodes[approvalIndex+1:] {
			if node.Kind != "document" {
				t.Errorf("recipe %q: step %q (%s) runs after the approval — only the handover document may follow a decision", name, node.ID, node.Kind)
			}
		}
	}
}

// TestRecurringTemplatesHaveGuides keeps the shipped template and its guide
// from drifting apart. The guide is where the inputs are explained, where the
// checks' limits are written down, and where the command to rerun next period
// lives; a template with no guide is a form with no instructions.
func TestRecurringTemplatesHaveGuides(t *testing.T) {
	index, err := os.ReadFile(filepath.Join(repoRoot, "docs", "workflows", "README.md"))
	if err != nil {
		t.Fatalf("read the workflow index: %v", err)
	}
	examples, err := os.ReadFile(filepath.Join(repoRoot, "examples", "repeatable-workflows", "README.md"))
	if err != nil {
		t.Fatalf("read the repeatable-workflows example: %v", err)
	}
	for _, name := range recurringTemplates {
		guidePath := filepath.Join(repoRoot, "docs", "workflows", name+".md")
		guide, err := os.ReadFile(guidePath)
		if err != nil {
			t.Errorf("recipe %q: no guide at docs/workflows/%s.md: %v", name, name, err)
			continue
		}
		if !strings.Contains(string(guide), "hyperplane workflow run "+name) {
			t.Errorf("recipe %q: its guide never shows `hyperplane workflow run %s`, which is how it is rerun next period", name, name)
		}
		if !strings.Contains(string(index), "("+name+".md)") {
			t.Errorf("recipe %q: docs/workflows/README.md does not link %s.md, so nobody finds it", name, name)
		}
		if !strings.Contains(string(examples), name) {
			t.Errorf("recipe %q: examples/repeatable-workflows/README.md does not mention it", name)
		}
	}
}
