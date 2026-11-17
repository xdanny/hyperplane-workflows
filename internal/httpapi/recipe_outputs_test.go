package httpapi

import (
	"context"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	"testing"
)

func TestTemplateOutputsDescribeTheEditableSDKStepsWithoutRegistry(t *testing.T) {
	templates := []store.WorkflowTemplate{{Spec: store.JSON(`{"kind":"nodes","nodes":[{"id":"plan","title":"Plan"},{"id":"approve","kind":"approval","title":"Approve"},{"id":"build","kind":"task","title":"Build baseline"},{"id":"review","kind":"check","title":"Review evidence"}]}`)}, {Spec: store.JSON(`{"nodes":[{"kind":"process","ref":"pr/retired"}]}`)}}
	// No Store is configured: this read must not query legacy process contracts.
	(&Server{}).describeRecipes(context.Background(), templates)
	got := templates[0].Outputs
	if len(got) != 3 || got[0].ForOwner != "Plan" || got[1].ForOwner != "Build baseline — files and a summary" || got[2].ForOwner != "Review evidence — review findings" {
		t.Fatalf("unexpected outputs: %+v", got)
	}
	if len(templates[1].Outputs) != 0 {
		t.Fatal("legacy graph was described as SDK output")
	}
}
