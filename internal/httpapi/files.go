package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleFile serves a file from within a run directory with traversal protection.
// Route: GET /file/{id}/{path...}
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}

	// path... captures everything after /file/{id}/
	relPath := r.PathValue("path")
	if strings.Contains(relPath, "..") {
		http.Error(w, "path traversal rejected", http.StatusForbidden)
		return
	}
	// An empty path would resolve to the run directory itself. That matters
	// beyond tidiness: ServeMux rewrites `/file/{id}/a/../../x` to `/file/x/`,
	// which would otherwise hand back <runsDir>/x — a file that is not part of
	// the named run at all. This route serves files inside one run, nothing else.
	if strings.Trim(relPath, "/") == "" {
		http.Error(w, "missing file path", http.StatusBadRequest)
		return
	}

	runDir, err := safeRunDir(s.cfg.RunsDir, id)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid path: %v", err), http.StatusBadRequest)
		return
	}

	// Resolve and confirm the file is under the run directory.
	target := filepath.Join(runDir, filepath.FromSlash(relPath))
	// Clean the path and ensure it stays under runDir.
	cleanTarget := filepath.Clean(target)
	cleanRunDir := filepath.Clean(runDir)
	if !strings.HasPrefix(cleanTarget, cleanRunDir+string(filepath.Separator)) &&
		cleanTarget != cleanRunDir {
		http.Error(w, "path traversal rejected", http.StatusForbidden)
		return
	}

	// Lexical cleaning alone still lets a symlink INSIDE the run dir point out of
	// it, so resolve both sides for real and compare the resolved paths. Both are
	// resolved because the runs dir itself is often reached through a symlink
	// (/tmp → /private/tmp on macOS) — comparing resolved against unresolved
	// would reject every legitimate read there.
	realRunDir, err := filepath.EvalSymlinks(cleanRunDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	realTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		// Missing file, or a dangling link — nothing to serve either way.
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(realTarget, realRunDir+string(filepath.Separator)) {
		http.Error(w, "path traversal rejected", http.StatusForbidden)
		return
	}
	// Directories are listed by the artifact/asset APIs, not browsed here.
	if fi, err := os.Stat(realTarget); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}

	http.ServeFile(w, r, realTarget)
}
