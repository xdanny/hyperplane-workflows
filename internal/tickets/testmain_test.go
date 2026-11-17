package tickets_test

import (
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	cleanup := dbtest.RedirectToTestDB("tickets_test")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
