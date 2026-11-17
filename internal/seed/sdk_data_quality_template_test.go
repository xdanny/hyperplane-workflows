package seed_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestQualityTemplateKeepsApprovalWithoutInventingBlockerRules(t *testing.T) {
	for _, template := range seed.DefaultTemplates {
		if template.Name != "data-quality-audit" {
			continue
		}
		var workflow store.SDKWorkflow
		if err := json.Unmarshal(template.Spec, &workflow); err != nil {
			t.Fatal(err)
		}
		planned, approved, audited := false, false, false
		for _, node := range workflow.Nodes {
			switch {
			case node.Acceptance == "data-quality-rules":
				planned = true
				if !strings.Contains(node.Instruction, "One complete rule is sufficient") {
					t.Fatal("template must permit a complete one-rule audit")
				}
			case node.Kind == "approval" && planned:
				approved = true
			case node.Acceptance == "data-quality-audit":
				audited = true
				if !approved {
					t.Fatal("audit must retain approval of proposed rules")
				}
				if strings.Contains(node.Instruction, "blocker violation") || !strings.Contains(node.Instruction, "any approved executable rule") || !strings.Contains(node.Instruction, "harness-reported") {
					t.Fatal("template must permit every approved severity and distinguish reported evidence")
				}
			}
		}
		if !audited {
			t.Fatal("missing quality audit check")
		}
		return
	}
	t.Fatal("missing quality template")
}
