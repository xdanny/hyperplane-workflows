package sdkruntime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

func TestSDKApprovalEvidenceFollowsDependenciesNotArrayPrefix(t *testing.T) {
	root := t.TempDir()
	nodes := []sdkworkflow.Node{
		{ID: "approve", Kind: "approval", DependsOn: []string{"branch"}},
		{ID: "unrelated", DependsOn: []string{}},
		{ID: "branch", DependsOn: []string{"source"}},
		{ID: "source", DependsOn: []string{}},
	}
	evidence, err := sdkEvidenceNodes(nodes, "approve")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, node := range evidence {
		ids = append(ids, node.ID)
	}
	if !reflect.DeepEqual(ids, []string{"branch", "source"}) {
		t.Fatalf("wrong evidence: %v", ids)
	}
	pins := []documentPinFixture{
		pinDocumentFixture(t, root, "branch", "branch result"),
		pinDocumentFixture(t, root, "source", "source evidence"),
		{ID: "unrelated", Phase: "pending"}, {ID: "approve", Phase: "approval_pending"},
	}
	req := codex.WorkflowRequest{Workspace: root, Nodes: nodes}
	raw := documentCheckpointFixture(t, pins)
	if err := verifySDKDocuments(t.Context(), req, raw, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := verifySDKDocuments(t.Context(), req, raw, ""); err == nil {
		t.Fatal("unfinished branch accepted at publication")
	}
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("changed source!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifySDKDocuments(t.Context(), req, raw, "approve"); err == nil {
		t.Fatal("changed ancestor accepted")
	}
}

func TestSDKEvidenceRejectsBrokenFrozenDependencies(t *testing.T) {
	for _, nodes := range [][]sdkworkflow.Node{
		{{ID: "a", DependsOn: []string{"missing"}}},
		{{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}}},
		{{ID: "a"}, {ID: "a"}},
	} {
		if _, err := sdkEvidenceNodes(nodes, "a"); err == nil {
			t.Fatal("invalid frozen dependency evidence accepted")
		}
	}
}
