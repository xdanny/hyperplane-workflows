package orchestrate

import (
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	cleanup := dbtest.RedirectToTestDB("orchestrate")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
