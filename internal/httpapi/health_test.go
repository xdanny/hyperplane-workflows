package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
)

func TestSDKHealthCachesInstallationChecks(t *testing.T) {
	override := filepath.Join(t.TempDir(), "codex-override")
	if err := os.WriteFile(override, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", override)
	health := sdkHealth{}
	if !health.check(t.Context()) {
		t.Fatal("installed runtime unavailable")
	}
	if err := os.Remove(override); err != nil {
		t.Fatal(err)
	}
	if !health.check(t.Context()) {
		t.Fatal("health polling bypassed cache")
	}
	health.checkedAt = time.Now().Add(-31 * time.Second)
	if health.check(t.Context()) {
		t.Fatal("expired health cache concealed missing executable")
	}
}

func TestSDKHealthDoesNotCacheCancelledProbe(t *testing.T) {
	health := sdkHealth{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if health.check(ctx) || !health.checkedAt.IsZero() {
		t.Fatal("cancelled request poisoned health cache")
	}
}

func TestUnsupportedEvalLaunchCannotAcceptWork(t *testing.T) {
	h := New(config.Config{}, nil).Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/evals/example/runs", nil)
	r.RemoteAddr = "127.0.0.1:1000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported launch: %d %s", w.Code, w.Body.String())
	}
}

// A cold start losing a race with its own probe used to report the SDK
// unavailable for the next half minute. A failure is now retried once and, if
// it still fails, believed only briefly.
func TestSDKHealthRetriesAndExpiresNegativeAnswersQuickly(t *testing.T) {
	override := filepath.Join(t.TempDir(), "codex-override")
	health := sdkHealth{}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", override)
	if health.check(t.Context()) {
		t.Fatal("a missing executable reported as installed")
	}
	if err := os.WriteFile(override, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Still inside the short negative window: the cached answer stands.
	if health.check(t.Context()) {
		t.Fatal("health polling bypassed the negative cache")
	}
	health.checkedAt = time.Now().Add(-(sdkHealthNegativeTTL + time.Second))
	if !health.check(t.Context()) {
		t.Fatal("a negative answer outlived its short cache")
	}
	// A positive answer is kept for much longer than a negative one.
	if err := os.Remove(override); err != nil {
		t.Fatal(err)
	}
	health.checkedAt = time.Now().Add(-(sdkHealthNegativeTTL + time.Second))
	if !health.check(t.Context()) {
		t.Fatal("a positive answer was discarded on the negative schedule")
	}
	health.checkedAt = time.Now().Add(-(sdkHealthPositiveTTL + time.Second))
	if health.check(t.Context()) {
		t.Fatal("an expired positive answer concealed a missing executable")
	}
}
