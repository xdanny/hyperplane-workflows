package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

func TestMain(m *testing.M) {
	cleanup := dbtest.RedirectToTestDB("cli")
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestTaskCLIUsesCheckedSDKAndExitStatus(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("test database not configured")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal", "codex", "testdata", "task"))
	t.Setenv("HYPERPLANE_RUNS_DIR", t.TempDir())
	for _, tc := range []struct {
		mode   string
		repair bool
		exit   int
	}{{"ordinary", false, 0}, {"recovery", true, 0}, {"recovery", false, 1}} {
		t.Setenv("SDK_FIXTURE_MODE", tc.mode)
		target := t.TempDir()
		args := []string{"--target", target, "--check", `["python3","-c","from pathlib import Path; assert Path('answer.txt').read_text().strip() == '42'"]`, "--json"}
		if tc.repair {
			args = append(args, "--auto-repair")
		}
		args = append(args, "Write the answer")
		if got := cmdTask(args); got != tc.exit {
			t.Fatalf("%s repair %v: exit %d want %d", tc.mode, tc.repair, got, tc.exit)
		}
		data, err := os.ReadFile(filepath.Join(target, "answer.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if tc.exit == 0 && string(data) != "42\n" {
			t.Fatalf("wrong successful artifact: %s", data)
		}
	}
}

func TestTaskCLIRejectsIncompleteArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--target", "/tmp", "--check", "{}", "goal"}, {"--target", "/tmp", "--check", "[]", "goal"}, {"--target", "/tmp", "--check", "null", "goal"}, {"--target", "/tmp", "--check", `[" "]`, "goal"}, {"--unknown"}} {
		if got := cmdTask(args); got != 2 {
			t.Fatalf("invalid arguments: %d", got)
		}
	}
}

func TestTaskCLIWithoutExtraCheck(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("test database not configured")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal", "codex", "testdata", "task"))
	t.Setenv("HYPERPLANE_RUNS_DIR", t.TempDir())
	t.Setenv("SDK_FIXTURE_MODE", "recovery")
	target := t.TempDir()
	if got := cmdTask([]string{"--target", target, "--json", "Write an answer"}); got != 0 {
		t.Fatalf("no-check task exit: %d", got)
	}
	// The fixture's first answer fails the optional check used above. Completion
	// without that check must neither claim correctness nor request a correction.
	data, err := os.ReadFile(filepath.Join(target, "answer.txt"))
	if err != nil || string(data) != "wrong\n" {
		t.Fatalf("output: %q, %v", data, err)
	}
}
