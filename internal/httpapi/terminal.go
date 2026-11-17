package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/coder/websocket"
	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/terminal"
)

// handleAuthCheck reports whether a token is required and whether THIS request is
// authenticated. It is intentionally reachable without auth (see authMiddleware)
// so the web login screen can probe the server and validate a stored token.
// GET /api/auth/check
func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	required := s.cfg.Token != ""
	authed := !required || tokenMatches(r, s.cfg.Token) || isTrustedLocal(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authRequired": required,
		"authed":       authed,
	})
}

// handleAuthLogin validates a token and, on success, sets an hp_token cookie so
// the browser authenticates every subsequent request (fetch, <img>, SSE, the
// terminal WebSocket) automatically and uniformly. Public route.
// POST /api/auth/login {"token": "…"}
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token == "" {
		// No token configured ⇒ nothing to log in to.
		writeJSON(w, http.StatusOK, map[string]any{"authRequired": false, "ok": true})
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(s.cfg.Token)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "hp_token",
		Value:    body.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60 * 60 * 24 * 30, // 30 days
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthLogout clears the hp_token cookie. Public route.
// POST /api/auth/logout
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "hp_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// isHTTPS reports whether the original client request used TLS, accounting for a
// terminating proxy / tunnel (X-Forwarded-Proto) in front of the loopback API.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// handleTerminalWS upgrades to a WebSocket and runs a PTY-backed terminal session.
// GET /api/terminal/ws?kind=shell|orchestrator&cols=N&rows=N&token=…
//
// Auth is enforced by authMiddleware before we get here. Origin is checked to
// prevent a malicious local page from opening a shell over loopback (CSRF).
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "shell" && kind != "orchestrator" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "terminal kind must be shell or orchestrator"})
		return
	}
	origins := append([]string{
		"localhost:5173", "127.0.0.1:5173",
		"localhost:4252", "127.0.0.1:4252",
	}, s.cfg.AllowedOrigins...)

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: origins})
	if err != nil {
		// Accept already wrote the HTTP error response.
		return
	}
	defer c.CloseNow()

	cols := atoiU16(r.URL.Query().Get("cols"))
	rows := atoiU16(r.URL.Query().Get("rows"))

	cfg := terminal.Config{
		ShellCmd:        s.cfg.ShellCmd,
		OrchestratorCmd: s.cfg.OrchestratorCmd,
		WorkDir:         s.cfg.PlatformRoot,
		Env: []string{
			// Shell/custom commands never inherit another console's capability.
			"HYPERPLANE_CONSOLE_API=",
			"HYPERPLANE_CONSOLE_TOKEN=",
			"HYPERPLANE_API=http://" + s.cfg.Addr,
			"HYPERPLANE_PLATFORM_ROOT=" + s.cfg.PlatformRoot,
			"HYPERPLANE_TARGET_ROOT=" + s.cfg.TargetRoot,
		},
	}
	done := make(chan struct{})
	if err := s.runner.launch(sdkruntime.NewRunID("console"), r.Context(), func(ctx context.Context) {
		defer close(done)
		if kind == "orchestrator" && s.cfg.OrchestratorCmd == filepath.Join(s.cfg.PlatformRoot, "scripts", "orchestrator.sh") {
			management, err := s.startConsoleManagement(ctx)
			if err != nil {
				_ = c.Close(websocket.StatusInternalError, "console management unavailable")
				return
			}
			defer management.Close()
			cfg.Env = append(cfg.Env, "HYPERPLANE_CONSOLE_API="+management.URL, "HYPERPLANE_CONSOLE_TOKEN="+management.Token)
		}
		_ = terminal.Serve(ctx, c, cfg, kind, cols, rows)
	}); err != nil {
		_ = c.Close(websocket.StatusGoingAway, "application is shutting down")
		return
	}
	<-done
}

func atoiU16(s string) uint16 {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 65535 {
		return 0
	}
	return uint16(n)
}
