package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
)

func newAuthServer(token string) http.Handler {
	s := &Server{cfg: config.Config{Token: token}}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return s.authMiddleware(ok)
}

func req(method, path, remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func status(h http.Handler, r *http.Request) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestAuth_TokenConfigured(t *testing.T) {
	h := newAuthServer("s3cret")
	cases := []struct {
		name string
		r    *http.Request
		want int
	}{
		{"loopback no headers ⇒ allowed", req("GET", "/api/runs", "127.0.0.1:5000", nil), 200},
		{"loopback but forwarded ⇒ remote, blocked", req("GET", "/api/runs", "127.0.0.1:5000", map[string]string{"Cf-Connecting-Ip": "1.2.3.4"}), 401},
		{"remote no token ⇒ blocked", req("GET", "/api/runs", "9.9.9.9:5000", nil), 401},
		{"remote bearer ok ⇒ allowed", req("GET", "/api/runs", "9.9.9.9:5000", map[string]string{"Authorization": "Bearer s3cret"}), 200},
		{"remote bearer wrong ⇒ blocked", req("GET", "/api/runs", "9.9.9.9:5000", map[string]string{"Authorization": "Bearer nope"}), 401},
		{"remote query token ok ⇒ allowed", req("GET", "/api/runs?token=s3cret", "9.9.9.9:5000", nil), 200},
		{"auth check always public", req("GET", "/api/auth/check", "9.9.9.9:5000", nil), 200},
	}
	for _, c := range cases {
		if got := status(h, c.r); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestAuth_NoTokenFailsClosedRemotely(t *testing.T) {
	h := newAuthServer("")
	if got := status(h, req("GET", "/api/runs", "127.0.0.1:5000", nil)); got != 200 {
		t.Errorf("loopback should be allowed when no token: got %d", got)
	}
	if got := status(h, req("GET", "/api/runs", "9.9.9.9:5000", nil)); got != http.StatusForbidden {
		t.Errorf("remote should be forbidden when no token configured: got %d", got)
	}
	if got := status(h, req("GET", "/api/runs", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "1.2.3.4"})); got != http.StatusForbidden {
		t.Errorf("forwarded loopback should be forbidden when no token: got %d", got)
	}
}

func TestTokenMatches_Cookie(t *testing.T) {
	r := req("GET", "/api/runs", "9.9.9.9:5000", nil)
	r.AddCookie(&http.Cookie{Name: "hp_token", Value: "s3cret"})
	if !tokenMatches(r, "s3cret") {
		t.Error("cookie token should match")
	}
	if tokenMatches(r, "other") {
		t.Error("cookie token should not match a different secret")
	}
}
