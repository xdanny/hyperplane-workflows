package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdeatorCLIUsesSDKAndExitStatus(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("test database not configured")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal", "codex", "testdata", "ideator"))
	for _, tc := range []struct {
		mode   string
		repair bool
		exit   int
	}{{"ordinary", false, 0}, {"recovery", true, 0}, {"recovery", false, 1}} {
		t.Run(tc.mode+map[bool]string{true: "-corrected", false: ""}[tc.repair], func(t *testing.T) {
			t.Setenv("SDK_FIXTURE_MODE", tc.mode)
			runs := t.TempDir()
			t.Setenv("HYPERPLANE_RUNS_DIR", runs)
			args := []string{"--json", "--project", "ideator-cli"}
			if tc.repair {
				args = append(args, "--auto-repair")
			}
			args = append(args, "cycling tools")
			if got := cmdIdeator(args); got != tc.exit {
				t.Fatalf("exit %d, want %d", got, tc.exit)
			}
			files, err := filepath.Glob(filepath.Join(runs, "*", "nodes", "delivery", "OUTPUTS", "ideas.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("managed output missing: %v (%v)", files, err)
			}
		})
	}
}

func TestIdeatorCLIRejectsGraphSettingsAndInvalidBriefs(t *testing.T) {
	for _, args := range [][]string{nil, {" "}, {"one", "two"}, {strings.Repeat("x", 4001)}, {"--target", "/tmp", "brief"}, {"--dry-run", "brief"}, {"--input", "domain=x", "brief"}, {"--auto-decompose", "brief"}} {
		if got := cmdIdeator(args); got != 2 {
			t.Fatalf("%v: exit %d", args, got)
		}
	}
}
