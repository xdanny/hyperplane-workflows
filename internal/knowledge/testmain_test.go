package knowledge

import (
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	cleanup := dbtest.RedirectToTestDB("knowledge")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
