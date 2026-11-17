package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

const maxLogBytes = 256 * 1024 // 256 KB

// maxTailLines bounds ?tail=N so a caller cannot ask for a line count large
// enough to defeat the byte cap's purpose. The byte cap still applies on top:
// a tail is always the last N lines *within* the final maxLogBytes.
const maxTailLines = 20000

// parseTailLines reads ?tail=N. The second result distinguishes "asked for a
// bounded tail" from "asked for the whole log" — a missing, malformed, or
// non-positive value means the latter, matching the endpoint's old behaviour.
func parseTailLines(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("tail")
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n > maxTailLines {
		n = maxTailLines
	}
	return n, true
}

// lastLines returns the trailing n lines of b, or b unchanged when it holds
// fewer than n lines. A single trailing newline is not counted as an empty
// final line, so tail=1 over "a\nb\n" yields "b\n" rather than "".
func lastLines(b []byte, n int) []byte {
	if n <= 0 || len(b) == 0 {
		return b
	}
	end := len(b)
	if b[end-1] == '\n' {
		end--
	}
	count := 0
	for i := end - 1; i >= 0; i-- {
		if b[i] != '\n' {
			continue
		}
		count++
		if count == n {
			return b[i+1:]
		}
	}
	return b
}

// handleGetLog returns the raw log text for a node.
//
//	GET /api/runs/{id}/log?nodeId=X[&tail=N]
//	GET /api/runs/{id}/log/{nodeId}[?tail=N]
//
// The response is text/plain. `tail=N` returns only the last N lines; without
// it the whole log comes back, capped at the last maxLogBytes. X-Log-Size
// always carries the full on-disk byte size and X-Log-Truncated is set whenever
// the body is less than the whole file — together they let a viewer say "showing
// the last 200 lines of 4.1 MB" and offer to fetch the rest.
func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID(id) {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}

	// Accept nodeId from path segment or query param.
	nodeID := r.PathValue("nodeId")
	if nodeID == "" {
		nodeID = r.URL.Query().Get("nodeId")
	}
	if nodeID == "" {
		http.Error(w, "nodeId is required", http.StatusBadRequest)
		return
	}

	if file := r.URL.Query().Get("file"); file != "" && file != "log" {
		http.Error(w, "unknown file kind", http.StatusBadRequest)
		return
	}

	cleanLog, err := nodeFilePath(s.cfg.RunsDir, id, nodeID, ".log")
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid path: %v", err), http.StatusBadRequest)
		return
	}

	f, err := os.Open(cleanLog)
	if os.IsNotExist(err) {
		http.Error(w, filepath.Base(cleanLog)+" not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("open log: %v", err), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("stat log: %v", err), http.StatusInternalServerError)
		return
	}

	size := info.Size()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Log-Size", strconv.FormatInt(size, 10))

	// Read at most the final maxLogBytes; every mode below works on that window,
	// so a multi-gigabyte log can never be pulled into memory.
	dropped := size > maxLogBytes
	if dropped {
		if _, err := f.Seek(size-maxLogBytes, io.SeekStart); err != nil {
			dropped = false // seek failed — fall back to reading from the start
		}
	}
	body, err := io.ReadAll(io.LimitReader(f, maxLogBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf("read log: %v", err), http.StatusInternalServerError)
		return
	}

	if n, ok := parseTailLines(r); ok {
		if tail := lastLines(body, n); len(tail) < len(body) {
			body = tail
			dropped = true
		}
	}

	if dropped {
		w.Header().Set("X-Log-Truncated", "true")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
