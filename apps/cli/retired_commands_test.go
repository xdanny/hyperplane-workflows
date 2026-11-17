package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise actual CLI dispatch, including os.Exit, without an application DB.
func TestCLIRetiredGraphCommands(t *testing.T) {
	if os.Getenv("HYPERPLANE_TEST_CLI_DISPATCH") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"hyperplane"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		t.Fatal("missing subprocess arguments")
	}
	for _, args := range [][]string{
		{"run", "hello"}, {"trigger", "hello"}, {"dags"}, {"validate", "old.json"},
		{"preflight", "old.json"}, {"processes"}, {"runs", "repair", "old-run"},
		{"optimize", "node", "old.json", "work"}, {"optimize", "nodes", "old.json"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIRetiredGraphCommands$", "--"}, args...)...)
			cmd.Env = append(os.Environ(), "HYPERPLANE_TEST_CLI_DISPATCH=1", "TEST_DATABASE_URL=", "DATABASE_URL=")
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("retired command did not fail before launch: exit=%v output=%s", err, output)
			}
			if !strings.Contains(string(output), "unknown") && !strings.Contains(string(output), "retired") {
				t.Fatalf("missing actionable rejection: %s", output)
			}
		})
	}
}
