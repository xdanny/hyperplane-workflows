package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticHandler serves the built React bundle (cfg.WebDir) as a single-page app:
// real files are served directly; any other path falls back to index.html so
// client-side routing works. This is PUBLIC (not behind the token) so the login
// screen can load; the API and artifacts it then calls remain protected. When no
// WebDir is configured the handler 404s, leaving the API-only posture intact.
func (s *Server) staticHandler() http.Handler {
	dir := s.cfg.WebDir
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dir == "" {
			http.NotFound(w, r)
			return
		}
		clean := filepath.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		full := filepath.Join(dir, clean)

		// Guard against path traversal escaping the bundle root.
		if rel, err := filepath.Rel(dir, full); err != nil || strings.HasPrefix(rel, "..") {
			http.NotFound(w, r)
			return
		}

		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			// Long-cache fingerprinted assets; never cache the app shell.
			if strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFile(w, r, full)
			return
		}

		// SPA fallback.
		index := filepath.Join(dir, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}
