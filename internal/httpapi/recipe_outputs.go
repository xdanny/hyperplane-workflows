package httpapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Template outputs come from the same editable SDK steps that will execute.
// There is no process-registry lookup or separately maintained graph contract.
func (s *Server) describeRecipes(_ context.Context, templates []store.WorkflowTemplate) {
	for i := range templates {
		var workflow store.SDKWorkflow
		if json.Unmarshal(templates[i].Spec, &workflow) != nil || workflow.Kind != "nodes" {
			continue
		}
		for _, node := range workflow.Nodes {
			title := strings.TrimSpace(node.Title)
			if title == "" || node.Kind == "approval" {
				continue
			}
			switch node.Kind {
			case "task":
				title += " — files and a summary"
			case "check":
				title += " — review findings"
			}
			templates[i].Outputs = append(templates[i].Outputs, store.RecipeOutput{Name: node.ID, ForOwner: title})
		}
	}
}
