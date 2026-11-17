package httpapi

import (
	"fmt"
	"path/filepath"
	"strings"
)

// nodeFilePath confines a step log to its run directory.
func nodeFilePath(runsDir, runID, nodeID, suffix string) (string, error) {
	if nodeID == "" || strings.Contains(nodeID, "/") || strings.Contains(nodeID, "..") ||
		strings.ContainsAny(nodeID, `\:*?"<>|`) {
		return "", fmt.Errorf("invalid node id")
	}
	runDir, err := safeRunDir(runsDir, runID)
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(runDir, nodeID+suffix))
	// Defence in depth: the resolved path must still sit under the run dir.
	if !strings.HasPrefix(path, filepath.Clean(runDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal rejected")
	}
	return path, nil
}
