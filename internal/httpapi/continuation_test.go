package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

func TestContinuationBodyCannotReplaceStoredWorkflow(t *testing.T) {
	h := New(config.Config{}, nil).Handler()
	token := strings.Repeat("a", 64)
	for _, body := range []string{
		"", `null`, `[]`, `{}`, `{"reviewToken":null}`, `{"reviewToken":""}`, `{"reviewToken":"invalid"}`,
		`{"reviewToken":"` + token + `","nodes":[]}`, `{"reviewToken":"` + token + `","index":1}`, `{"reviewToken":"` + token + `","sourceRunId":"other"}`,
		`{"reviewToken":"` + token + `"} {}`, `{"reviewToken":"` + token + `"}` + strings.Repeat(" ", 1<<20),
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/runs/stopped/continuation", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body admitted without review/storage: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestContinuationErrorStatusesPreserveWrappedFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{store.ErrContinuationCommitUnknown, 500}, {store.ErrNotFound, 404}, {store.ErrContinuationChanged, 409}, {store.ErrRunActive, 409},
		{agentchecks.ErrContinuationBlocked, 409}, {agentchecks.ErrValidationUnavailable, 503}, {fmt.Errorf("unexpected failure"), 500},
	} {
		if got := continuationErrorStatus(fmt.Errorf("context: %w", tc.err)); got != tc.status {
			t.Fatalf("%v: %d", tc.err, got)
		}
	}
}
