package brief

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// skillDefPath is the seeded skill this package embeds a copy of.
const skillDefPath = "../../knowledge/defs/skills/human-translation.json"

// TestEmbeddedInstructionMatchesTheSeededSkill is the anti-drift guard.
//
// The stored skill is the writing standard every brief is held to; the embedded
// copy exists only so an unseeded database still writes to a standard. Two
// copies of a standard that disagree is worse than one — a brief written on a
// fresh clone would silently be held to different rules than one written after
// `make seed`.
func TestEmbeddedInstructionMatchesTheSeededSkill(t *testing.T) {
	b, err := os.ReadFile(skillDefPath)
	if err != nil {
		t.Fatalf("read %s: %v", skillDefPath, err)
	}
	var def struct {
		ID          string `json:"id"`
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal(b, &def); err != nil {
		t.Fatalf("parse %s: %v", skillDefPath, err)
	}
	if def.ID != SkillID {
		t.Errorf("the skill file's id is %q, want %q", def.ID, SkillID)
	}
	if norm(def.Instruction) != norm(EmbeddedInstruction) {
		t.Errorf("the embedded instruction has drifted from %s — copy the file's `instruction` into skill.go", skillDefPath)
	}
}

// TestInstructionEncodesTheRulesBriefsDependOn: the prompt assembles the
// skill's own words, so the rules the platform enforces elsewhere (the
// grounding check, the four parts, the no-softening rule) have to actually be
// stated in it.
func TestInstructionEncodesItsRules(t *testing.T) {
	instr := norm(EmbeddedInstruction)
	for _, want := range []string{
		"WHAT THIS WORKFLOW SET OUT TO BUILD",
		"WHAT ACTUALLY HAPPENED",
		"THE TRUE OUTCOME",
		"WHAT TO DO NEXT",
		"NAME THE KIND OF THING EVERY STATUS REFERS TO",
		"Every number must come from the STATE DOCUMENT",
		"Softening a finding is falsification",
		"IF THE STATE DOES NOT SAY, WRITE 'not recorded'",
		"DEFINE TERMS OF ART AT FIRST USE",
		"Ordinary file production does not establish independently checked correctness",
		"a model review is a fallible judgment",
		"without borrowing conclusions from other attempts",
		"Artifact contents omitted from the brief are unexamined, not absent",
		"never changes_requested",
		"never refer to 'this state document'",
		"WHEN THE SUBJECT IS A DELIVERY, the verdict of 'what actually happened' is the HUMAN'S DELIVERY DECISION",
	} {
		if !strings.Contains(instr, want) {
			t.Errorf("the human-translation instruction does not state: %q", want)
		}
	}
}

// norm collapses whitespace so the line wrapping of the embedded copy is not
// mistaken for a change in the text.
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }
