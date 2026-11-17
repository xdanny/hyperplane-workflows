package sdkruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

type documentPinFixture struct {
	ID       string                    `json:"id"`
	Phase    string                    `json:"phase"`
	Document *documentInventoryFixture `json:"document,omitempty"`
}

type documentInventoryFixture struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func pinDocumentFixture(t *testing.T, root, id, text string) documentPinFixture {
	t.Helper()
	path := id + ".md"
	if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return documentPinFixture{ID: id, Phase: "passed", Document: &documentInventoryFixture{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Bytes: int64(len(text))}}
}

func documentCheckpointFixture(t *testing.T, pins []documentPinFixture) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"nodes": pins})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSDKDocumentsRejectChangedOrInvalidPins(t *testing.T) {
	for _, scenario := range []string{"same-size tamper", "missing file", "missing step", "missing inventory", "duplicate step", "uncompleted step", "wrong filename", "traversal path", "absolute path", "empty file", "oversized pin", "wrong byte count", "missing hash", "symlink inside", "symlink outside", "hardlink", "directory"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			req := codex.WorkflowRequest{Workspace: root, Nodes: []sdkworkflow.Node{{ID: "report", Kind: "document"}}}
			pin := pinDocumentFixture(t, root, "report", "approved report")
			pins := []documentPinFixture{pin}
			if err := verifySDKDocuments(t.Context(), req, documentCheckpointFixture(t, pins), ""); err != nil {
				t.Fatalf("valid document rejected: %v", err)
			}
			file := filepath.Join(root, "report.md")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "same-size tamper":
				must(os.WriteFile(file, []byte("modified report"), 0o600))
			case "missing file":
				must(os.Remove(file))
			case "missing step":
				pins = nil
			case "missing inventory":
				pins[0].Document = nil
			case "duplicate step":
				pins = append(pins, pin)
			case "uncompleted step":
				pins[0].Phase = "running"
			case "wrong filename":
				pins[0].Document.Path = "other.md"
			case "traversal path":
				pins[0].Document.Path = "../report.md"
			case "absolute path":
				pins[0].Document.Path = file
			case "empty file":
				must(os.WriteFile(file, nil, 0o600))
				pins[0].Document.Bytes = 0
			case "oversized pin":
				pins[0].Document.Bytes = 1_000_001
			case "wrong byte count":
				pins[0].Document.Bytes++
			case "missing hash":
				pins[0].Document.SHA256 = ""
			case "symlink inside", "symlink outside":
				target := filepath.Join(root, "target.md")
				if scenario == "symlink outside" {
					target = filepath.Join(t.TempDir(), "target.md")
				}
				must(os.Rename(file, target))
				must(os.Symlink(target, file))
			case "hardlink":
				must(os.Link(file, filepath.Join(root, "alias.md")))
			case "directory":
				must(os.Remove(file))
				must(os.Mkdir(file, 0o700))
			}
			if err := verifySDKDocuments(t.Context(), req, documentCheckpointFixture(t, pins), ""); err == nil {
				t.Fatal("invalid document remained eligible for approval/publication")
			}
		})
	}
}

func TestSDKDocumentsApprovalChecksOnlyCompletedPrefix(t *testing.T) {
	root := t.TempDir()
	req := codex.WorkflowRequest{Workspace: root, Nodes: []sdkworkflow.Node{
		{ID: "plan", Kind: "document"}, {ID: "approve", Kind: "approval"}, {ID: "later", Kind: "document"},
	}}
	plan := pinDocumentFixture(t, root, "plan", "review this plan")
	// Approval and future work have no documents yet. They must not block
	// presenting the completed prefix, nor satisfy a final-delivery check.
	pins := []documentPinFixture{plan, {ID: "approve", Phase: "approval_pending"}, {ID: "later", Phase: "pending"}}
	raw := documentCheckpointFixture(t, pins)
	if err := verifySDKDocuments(t.Context(), req, raw, "approve"); err != nil {
		t.Fatalf("approval required future documents: %v", err)
	}
	if err := verifySDKDocuments(t.Context(), req, raw, ""); err == nil {
		t.Fatal("unfinished workflow passed final document verification")
	}
	if err := os.WriteFile(filepath.Join(root, "plan.md"), []byte("altered plan!!!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifySDKDocuments(t.Context(), req, raw, "approve"); err == nil {
		t.Fatal("approval accepted changed prior document")
	}
	pins[0] = pinDocumentFixture(t, root, "plan", "review this plan")
	pins[1] = pinDocumentFixture(t, root, "approve", "Human approved")
	pins[2] = pinDocumentFixture(t, root, "later", "final report")
	if err := verifySDKDocuments(t.Context(), req, documentCheckpointFixture(t, pins), ""); err != nil {
		t.Fatalf("complete pinned delivery rejected: %v", err)
	}
}

func TestSDKDocumentsMalformedStateAndCancellation(t *testing.T) {
	root := t.TempDir()
	req := codex.WorkflowRequest{Workspace: root, Nodes: []sdkworkflow.Node{{ID: "report"}}}
	if err := verifySDKDocuments(t.Context(), req, json.RawMessage(`{"nodes":`), ""); err == nil {
		t.Fatal("malformed checkpoint accepted")
	}
	pin := pinDocumentFixture(t, root, "report", "report")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verifySDKDocuments(ctx, req, documentCheckpointFixture(t, []documentPinFixture{pin}), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification: %v", err)
	}
}
