package optimize

import (
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	// A finished run fires the outcome-brief hook (internal/engine/brief.go), which
	// would otherwise make a BILLED engine call from a test. Switching the engine
	// half off leaves the trigger itself intact — the deterministic brief is still
	// written — which is exactly what the trigger tests assert.
	os.Setenv(config.BriefsEnvVar, "0")
	cleanup := dbtest.RedirectToTestDB("optimize")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
