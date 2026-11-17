package httpapi

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// authMiddleware enforces the bearer-token gate that lets Hyperplane be reached
// safely from outside loopback (e.g. via a Cloudflare Tunnel) without abandoning
// the "loopback-only, no auth" posture for genuine local use.
//
// Policy:
//   - token configured (HYPERPLANE_TOKEN set):
//   - a valid token (header / query / cookie) ⇒ allow.
//   - otherwise a *trusted local* request (loopback peer AND no proxy/forwarding
//     headers) ⇒ allow, so the local CLI and dev server need no token.
//   - everything else ⇒ 401.
//   - token not configured (default): trusted-local ⇒ allow; anything that looks
//     remote ⇒ 403 with a hint to set a token. This preserves the golden rule by
//     default and fails closed.
//
// A Cloudflare Tunnel dials the API from 127.0.0.1, so the loopback peer check
// alone would wrongly trust it — hence forwarded requests (Cf-Connecting-Ip,
// X-Forwarded-*) are always treated as remote regardless of peer address.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth bootstrap routes are always public so the web login screen can
		// discover whether a token is required and exchange a token for a cookie.
		switch r.URL.Path {
		case "/api/auth/check", "/api/auth/login", "/api/auth/logout":
			next.ServeHTTP(w, r)
			return
		}
		if s.cfg.Token != "" && tokenMatches(r, s.cfg.Token) {
			next.ServeHTTP(w, r)
			return
		}
		if isTrustedLocal(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.cfg.Token == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "remote access is disabled; set HYPERPLANE_TOKEN to enable authenticated remote use",
			})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="hyperplane"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid token"})
	})
}

// isTrustedLocal reports whether the request is a direct loopback request that
// did NOT arrive through a proxy/tunnel (which would add forwarding headers).
func isTrustedLocal(r *http.Request) bool {
	if isForwarded(r) {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isForwarded reports whether the request carries headers a reverse proxy or
// Cloudflare Tunnel adds — a signal it originated remotely.
func isForwarded(r *http.Request) bool {
	for _, h := range []string{
		"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
		"Cf-Connecting-Ip", "Cf-Ray", "Forwarded",
	} {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// tokenMatches checks the bearer token from the Authorization header, an
// X-Hyperplane-Token header, the `token` query param (needed for WebSocket and
// EventSource, which cannot set headers from the browser), or an hp_token cookie.
func tokenMatches(r *http.Request, want string) bool {
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	if got == "" {
		got = r.Header.Get("X-Hyperplane-Token")
	}
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got == "" {
		if c, err := r.Cookie("hp_token"); err == nil {
			got = c.Value
		}
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
