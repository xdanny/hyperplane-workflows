package sdkruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

// Keep these data quotas aligned with packages/agents/src/workflows/files.mjs.
const sdkMaxFileBytes int64 = 2_000_000_000
const sdkMaxStepBytes int64 = 8_000_000_000

// Resolve the evidence selected by a frozen SDK definition. Workflow validation
// and scheduling remain in the SDK; the host uses these edges only to pin the
// same prerequisite artifacts for a human decision. Empty before means all work
// must be present for final publication, including disconnected branches.
func sdkEvidenceNodes(nodes []sdkworkflow.Node, before string) ([]sdkworkflow.Node, error) {
	if before == "" {
		return nodes, nil
	}
	positions := make(map[string]int, len(nodes))
	for i, node := range nodes {
		if _, exists := positions[node.ID]; exists {
			return nil, fmt.Errorf("duplicate SDK evidence node: %s", node.ID)
		}
		positions[node.ID] = i
	}
	selected, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		index, exists := positions[id]
		if !exists {
			return fmt.Errorf("missing SDK evidence dependency: %s", id)
		}
		if visiting[id] {
			return fmt.Errorf("cyclic SDK evidence dependency: %s", id)
		}
		if selected[id] {
			return nil
		}
		visiting[id] = true
		deps := nodes[index].DependsOn
		if deps == nil && index > 0 {
			deps = []string{nodes[index-1].ID}
		}
		for _, dependency := range deps {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		selected[id] = true
		return nil
	}
	if err := visit(before); err != nil {
		return nil, err
	}
	delete(selected, before)
	var evidence []sdkworkflow.Node
	for _, node := range nodes {
		if selected[node.ID] {
			evidence = append(evidence, node)
		}
	}
	return evidence, nil
}

// Verify each completed document against its committed inventory before it is
// offered for approval or published. These pins also define the evidence a
// future continuation must preserve; a session ID alone cannot establish that.
func verifySDKDocuments(ctx context.Context, req codex.WorkflowRequest, raw json.RawMessage, before string) error {
	nodes, err := sdkEvidenceNodes(req.Nodes, before)
	if err != nil {
		return err
	}
	var state struct {
		Nodes []struct {
			ID, Phase string
			Document  *struct {
				Path, SHA256 string
				Bytes        int64
			}
		}
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	root, err := os.OpenRoot(req.Workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	buffer := make([]byte, 256*1024)
	for _, node := range nodes {
		found := false
		for _, step := range state.Nodes {
			if step.ID != node.ID {
				continue
			}
			if found || step.Phase != "passed" || step.Document == nil {
				return fmt.Errorf("completed document inventory missing or invalid: %s", node.ID)
			}
			found = true
			doc := step.Document
			if doc.Path != node.ID+".md" || doc.Bytes < 1 || doc.Bytes > 1_000_000 {
				return fmt.Errorf("invalid document inventory: %s", node.ID)
			}
			digest, err := hashSDKWorkFile(ctx, root, doc.Path, doc.Bytes, buffer)
			if err != nil {
				return err
			}
			if digest != doc.SHA256 {
				return fmt.Errorf("completed document changed: %s", node.ID)
			}
		}
		if !found {
			return fmt.Errorf("completed document inventory missing: %s", node.ID)
		}
	}
	return nil
}

// The worker records checked file hashes. The host reopens them beneath the
// managed root before publishing links or asking for approval.
func sdkWorkFiles(ctx context.Context, req codex.WorkflowRequest, raw json.RawMessage, before, runID string) ([]map[string]string, error) {
	nodes, err := sdkEvidenceNodes(req.Nodes, before)
	if err != nil {
		return nil, err
	}
	var state struct {
		Nodes []struct {
			ID    string
			Files []struct {
				Path, SHA256 string
				Bytes        int64
			}
		}
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(req.Workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	documents := []map[string]string{}
	buffer := make([]byte, 256*1024)
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if node.Kind != "task" {
			continue
		}
		found := false
		for _, step := range state.Nodes {
			if step.ID != node.ID {
				continue
			}
			if found || len(step.Files) == 0 || len(step.Files) > 1000 {
				return nil, fmt.Errorf("invalid checked work inventory")
			}
			found = true
			seen := map[string]bool{}
			var bytes int64
			for _, entry := range step.Files {
				if !filepath.IsLocal(entry.Path) || filepath.Clean(entry.Path) != entry.Path || strings.Contains(entry.Path, "\\") || !strings.HasPrefix(entry.Path, node.ID+"/") || seen[entry.Path] || entry.Bytes < 0 || entry.Bytes > sdkMaxFileBytes {
					return nil, fmt.Errorf("invalid checked work file")
				}
				seen[entry.Path] = true
				bytes += entry.Bytes
				if bytes > sdkMaxStepBytes {
					return nil, fmt.Errorf("checked work exceeds 8 GB per step")
				}
			}
			for _, entry := range step.Files {
				digest, err := hashSDKWorkFile(ctx, root, entry.Path, entry.Bytes, buffer)
				if err != nil {
					return nil, err
				}
				if digest != entry.SHA256 {
					return nil, fmt.Errorf("checked work file changed: %s", entry.Path)
				}
				parts := strings.Split(entry.Path, "/")
				for i := range parts {
					parts[i] = url.PathEscape(parts[i])
				}
				documents = append(documents, map[string]string{"name": entry.Path, "file": entry.Path, "bytes": strconv.FormatInt(entry.Bytes, 10), "sha256": entry.SHA256, "url": "/file/" + url.PathEscape(runID) + "/nodes/delivery/OUTPUTS/" + strings.Join(parts, "/")})
			}
		}
		if !found {
			return nil, fmt.Errorf("checked work inventory missing: %s", node.ID)
		}
	}
	return documents, nil
}

// Stream under the rooted file handle; never allocate an artifact-sized buffer.
func hashSDKWorkFile(ctx context.Context, root *os.Root, name string, size int64, buffer []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() != size || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return "", fmt.Errorf("checked work file changed: %s", name)
	}
	var digest string
	for pass := 0; pass < 2; pass++ {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		hash := sha256.New()
		var length int64
		for {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			n, err := file.Read(buffer[:min(int64(len(buffer)), size-length+1)])
			length += int64(n)
			if length > size {
				return "", fmt.Errorf("checked work file grew: %s", name)
			}
			hash.Write(buffer[:n])
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
		}
		current := fmt.Sprintf("%x", hash.Sum(nil))
		if length != size || (pass > 0 && current != digest) {
			return "", fmt.Errorf("checked work file changed during inspection: %s", name)
		}
		digest = current
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, after) || after.Size() != size || !after.ModTime().Equal(info.ModTime()) || after.Sys().(*syscall.Stat_t).Nlink != 1 {
		return "", fmt.Errorf("checked work file changed during inspection: %s", name)
	}
	return digest, nil
}
