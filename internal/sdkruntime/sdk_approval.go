package sdkruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// The host pins the actual documents before asking a person, then checks their
// hashes again before acknowledging approval. Model text cannot approve itself.
func awaitSDKApproval(ctx context.Context, st *store.Store, runID string, req codex.WorkflowRequest, raw json.RawMessage, save func(json.RawMessage) error) (string, error) {
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", err
	}
	current, _ := state["currentNode"].(string)
	index := -1
	for i, n := range req.Nodes {
		if n.ID == current && n.Kind == "approval" {
			index = i
		}
	}
	if req.Workflow != "node-workflow" || index < 0 || state["phase"] != "approval_pending" {
		return "", fmt.Errorf("invalid SDK approval step")
	}
	evidence, err := sdkEvidenceNodes(req.Nodes, current)
	if err != nil || len(evidence) == 0 {
		return "", fmt.Errorf("approval needs completed dependency evidence")
	}
	if err := verifySDKDocuments(ctx, req, raw, current); err != nil {
		return "", err
	}
	documents := []map[string]string{}
	for _, n := range evidence {
		data, err := readSDKArtifact(filepath.Join(req.Workspace, n.ID+".md"))
		if err != nil {
			return "", err
		}
		documents = append(documents, map[string]string{"name": n.Title, "file": n.ID + ".md", "sha256": fmt.Sprintf("%x", sha256.Sum256(data)), "url": "/file/" + runID + "/nodes/delivery/OUTPUTS/" + n.ID + ".md"})
	}
	workFiles, err := sdkWorkFiles(ctx, req, raw, current, runID)
	if err != nil {
		return "", err
	}
	documents = append(documents, workFiles...)
	id := runID + ":" + current + ":approval"
	state["approval"] = map[string]any{"id": id, "title": req.Nodes[index].Title, "prompt": req.Nodes[index].Instruction, "documents": documents, "gateNode": current, "dag": "node-workflow"}
	pinned, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	if err := save(pinned); err != nil {
		return "", err
	}
	timer := time.NewTicker(200 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
		}
		record, err := st.GetRecord(ctx, "approvals", id)
		if err != nil {
			return "", err
		}
		if record.State == "pending" {
			continue
		}
		if record.State != "approved" && record.State != "rejected" {
			return "", fmt.Errorf("approval request was dismissed")
		}
		if record.State == "approved" {
			if _, err := sdkWorkFiles(ctx, req, raw, current, runID); err != nil {
				return "", err
			}
			for _, doc := range documents {
				if filepath.Dir(doc["file"]) != "." {
					continue
				} // Work files were checked through the rooted reader above.
				data, err := readSDKArtifact(filepath.Join(req.Workspace, doc["file"]))
				if err != nil {
					return "", err
				}
				if fmt.Sprintf("%x", sha256.Sum256(data)) != doc["sha256"] {
					return "", fmt.Errorf("reviewed document changed: %s; start a new run for a fresh decision", doc["name"])
				}
			}
		}
		if err := st.ConsumeSDKApproval(ctx, runID, id); err != nil {
			return "", err
		}
		return record.State, nil
	}
}
