package httpapi

import (
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	// Outcome prose is deterministic unless a test supplies a model response.
	os.Setenv(config.BriefsEnvVar, "0")
	cleanup := dbtest.RedirectToTestDB("httpapi")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
