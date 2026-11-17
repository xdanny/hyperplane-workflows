package seed_test

import (
	"os"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	cleanup := dbtest.RedirectToTestDB("seed")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
