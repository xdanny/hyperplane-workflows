package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
)

// A legacy launch must fail before storage, tools, or model calls are possible.
func TestHTTPRetiredAdmissionStopsBeforeExecution(t *testing.T) {
	s := New(config.Config{}, nil)
	for _, body := range []string{
		`{"dag":"hello"}`,
		`{"dag":"app-ideator"}`,
		`{"workflow":{"name":"old","nodes":[{"id":"before","kind":"shell","run":"exit 0"}]}}`,
		`{"goal":"do work","nodes":[{"id":"task","kind":"task","title":"Work","instruction":"Write a file"}],"dryRun":true}`,
		`{"workflow":"app-ideator","autoDecompose":true}`,
		`{"workflow":"unknown"}`,
	} {
		w := httptest.NewRecorder()
		s.handlePostRun(w, httptest.NewRequest("POST", "/api/run", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("legacy launch reached execution: %s: %d %s", body, w.Code, w.Body.String())
		}
	}
}

func TestRetiredGraphRoutesAreAbsent(t *testing.T) {
	h := New(config.Config{}, nil).Handler()
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/dags"}, {"GET", "/api/dags/hello"},
		{"GET", "/api/connectors"},
		{"POST", "/api/validate"}, {"POST", "/api/preflight"},
		{"GET", "/api/processes"}, {"POST", "/api/processes"},
		{"POST", "/api/processes/example/promote"}, {"POST", "/api/processes/example/deprecate"},
		{"GET", "/api/runs/example/graph"}, {"POST", "/api/runs/example/repair"},
		{"GET", "/api/runs/example/failures"}, {"GET", "/api/runs/example/outputs"},
		{"GET", "/api/runs/example/logs"}, {"GET", "/api/runs/example/assets"},
		{"POST", "/api/runs/example/assets/image/vote"},
	} {
		r := httptest.NewRequest(route.method, route.path, nil)
		r.RemoteAddr = "127.0.0.1:1000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: expected retired route 404, got %d", route.method, route.path, w.Code)
		}
	}
}
