package store

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Reusable inputs let one saved definition run again with new values instead of
// being edited in N places. They are the SAME declaration templates already use
// (ContextSlot) and the SAME substitution (FillSlots), so a template prepared
// into a saved workflow keeps working exactly as it did as a template.
//
// Declaring an input is not a model call and not a run: saving still only
// validates and stores. Values arrive at launch.

// workflowInputID is the id syntax a saved workflow input must use. It is the
// token body, so it stays addressable inside `{slot:<id>}` without escaping.
//
// Capitals are accepted because the shipped templates already name their slots
// keyColumns and competitorsHint, and a template prepared into a saved workflow
// carries those same ids. New ids read better in lowercase; refusing the
// existing ones would only break the path this feature exists to open.
var workflowInputID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// slotToken finds every `{slot:<id>}` token, INCLUDING ids that are not valid
// and not declared — an unknown token is the mistake this scan exists to name.
var slotToken = regexp.MustCompile(`\{slot:([^{}]{1,80})\}`)

// maxWorkflowInputs bounds a declaration list so one saved row cannot grow
// without limit. It matches the column constraint in migration 0033.
const maxWorkflowInputs = 32

// UnknownInputsError reports `{slot:x}` tokens that name no declared input.
// The ids are returned to the caller so an editor can point at each one.
type UnknownInputsError struct {
	Unknown []string
}

func (e *UnknownInputsError) Error() string {
	return "these steps use inputs that are not declared: " + strings.Join(e.Unknown, ", ") +
		". Declare each one under the workflow's inputs, or remove the token."
}

// ValidateWorkflowInputs checks the declaration list and every token that
// refers to it. It reads only the submitted definition: no model, no run, no
// filesystem. A declared input that no token references is allowed — FillSlots
// carries its value into the workflow as a context block rather than dropping it.
func ValidateWorkflowInputs(w SDKWorkflow) error {
	if len(w.Inputs) > maxWorkflowInputs {
		return fmt.Errorf("%w: declare at most %d inputs", ErrInvalidWorkflow, maxWorkflowInputs)
	}
	seen := map[string]bool{}
	for _, in := range w.Inputs {
		if !workflowInputID.MatchString(in.ID) {
			return fmt.Errorf("%w: input id %q must start with a letter and use lowercase letters, digits, hyphen or underscore (up to 64 bytes)", ErrInvalidWorkflow, in.ID)
		}
		if seen[in.ID] {
			return fmt.Errorf("%w: input id %q is declared twice", ErrInvalidWorkflow, in.ID)
		}
		seen[in.ID] = true
		if len(in.Label) > 160 || len(in.Hint) > 2000 || len(in.Example) > 2000 {
			return fmt.Errorf("%w: input %q needs a label under 160 bytes and hint/example under 2000 bytes", ErrInvalidWorkflow, in.ID)
		}
		switch in.SlotType() {
		case SlotTypeText, SlotTypeList, SlotTypeDate, SlotTypeNumber, SlotTypeTable:
		default:
			return fmt.Errorf("%w: input %q has an unsupported type %q", ErrInvalidWorkflow, in.ID, in.Type)
		}
	}
	unknown := map[string]bool{}
	for _, text := range workflowSlotTexts(w) {
		for _, id := range slotTokenIDs(text) {
			if !seen[id] {
				unknown[id] = true
			}
		}
	}
	if len(unknown) > 0 {
		ids := make([]string, 0, len(unknown))
		for id := range unknown {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return &UnknownInputsError{Unknown: ids}
	}
	return nil
}

// workflowSlotTexts returns every string a `{slot:x}` token may appear in: the
// overall goal, each step's instruction, each step's declared source input
// values and each readable-input path. These are the four places FillSlots
// substitutes for a saved workflow.
func workflowSlotTexts(w SDKWorkflow) []string {
	texts := []string{w.Instruction}
	for _, node := range w.Nodes {
		texts = append(texts, node.Instruction)
		for _, value := range node.Inputs {
			texts = append(texts, value)
		}
		if node.Access != nil {
			texts = append(texts, node.Access.ReadPaths...)
		}
	}
	return texts
}

func slotTokenIDs(text string) []string {
	var out []string
	for _, match := range slotToken.FindAllStringSubmatch(text, -1) {
		out = append(out, match[1])
	}
	return out
}

// referencedWorkflowInputs reports which declared ids a token actually names.
func referencedWorkflowInputs(w SDKWorkflow) map[string]bool {
	declared := map[string]bool{}
	for _, in := range w.Inputs {
		declared[in.ID] = true
	}
	referenced := map[string]bool{}
	for _, text := range workflowSlotTexts(w) {
		for _, id := range slotTokenIDs(text) {
			if declared[id] {
				referenced[id] = true
			}
		}
	}
	return referenced
}

// FillWorkflowSlots substitutes this run's values into a saved definition.
//
// It reuses store.FillSlots — the same substitution templates use — so a
// workflow prepared from a template behaves identically once saved. Required
// inputs left blank return *MissingSlotsError; the caller turns that into the
// 400 the template endpoint already returns. Returned values are the accepted
// ones (declared, non-blank, normalized), which is what the launch record
// stores.
func FillWorkflowSlots(w SDKWorkflow, values map[string]string) (SDKWorkflow, map[string]string, error) {
	accepted := FilterSlotValues(w.Inputs, values)
	if missing := MissingSlots(w.Inputs, values); len(missing) > 0 {
		return w, accepted, &MissingSlotsError{Missing: missing}
	}
	if len(w.Inputs) == 0 {
		return w, accepted, nil
	}
	declarations := w.Inputs
	spec := w
	spec.Inputs = nil // Declarations are not prompt text; never substitute into them.
	raw, err := json.Marshal(spec)
	if err != nil {
		return w, accepted, err
	}
	filled, err := FillSlots(JSON(raw), declarations, accepted)
	if err != nil {
		return w, accepted, err
	}
	var out SDKWorkflow
	if err := json.Unmarshal(filled, &out); err != nil {
		return w, accepted, err
	}
	out.Inputs = declarations
	// FillSlots carries unreferenced values as a context block on the root STEP.
	// A workflow with no steps (a saved project task or app ideas) has no such
	// step, so the same block goes on its goal rather than being dropped.
	if out.Kind != "nodes" {
		referenced := referencedWorkflowInputs(w)
		var unreferenced []ContextSlot
		for _, in := range declarations {
			if !referenced[in.ID] && strings.TrimSpace(accepted[in.ID]) != "" {
				unreferenced = append(unreferenced, in)
			}
		}
		if len(unreferenced) > 0 {
			out.Instruction += contextBlock(unreferenced, accepted)
		}
	}
	return out, accepted, nil
}

// SlotLabels pairs each accepted input value with the label the person was
// asked for it under. Ids are for machines; a label is what the question said.
// An input with no label keeps its id, so a value is never left unnamed.
func SlotLabels(declarations []ContextSlot, accepted map[string]string) map[string]string {
	if len(accepted) == 0 {
		return nil
	}
	labels := map[string]string{}
	for _, slot := range declarations {
		if _, ok := accepted[slot.ID]; !ok {
			continue
		}
		label := strings.TrimSpace(slot.Label)
		if label == "" {
			label = slot.ID
		}
		labels[slot.ID] = label
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}
