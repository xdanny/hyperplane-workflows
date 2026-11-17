package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Exercise actual CLI dispatch for the saved-workflow verbs, including os.Exit,
// without an application database or a running API. Every case here is an
// ARGUMENT mistake: it must be rejected before the command reaches the network,
// and the rejection must say what to type instead.
func TestCLIWorkflowArgumentErrors(t *testing.T) {
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
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"workflow"}, "list|show|run|runs|attach"},
		{[]string{"workflow", "schedule"}, "unknown workflow sub-command"},
		{[]string{"workflow", "show"}, "workflow show <id|name>"},
		{[]string{"workflow", "show", "--nope", "brief"}, "unknown flag"},
		{[]string{"workflow", "run"}, "workflow run <id|name>"},
		{[]string{"workflow", "run", "brief", "--input"}, "workflow run <id|name>"},
		{[]string{"workflow", "run", "brief", "--input", "week"}, "needs key=value"},
		{[]string{"workflow", "run", "brief", "--project"}, "workflow run <id|name>"},
		{[]string{"workflow", "runs"}, "workflow runs <id|name>"},
		{[]string{"workflow", "runs", "brief", "--limit", "zero"}, "positive number"},
		{[]string{"workflow", "list", "--project"}, "workflow list"},
		{[]string{"workflow", "list", "--nope"}, "unknown flag"},
		{[]string{"workflow", "attach"}, "workflow attach <id|name>"},
		{[]string{"workflow", "attach", "brief", "--run"}, "--run needs a run id"},
		{[]string{"workflow", "attach", "brief", "--nope"}, "unknown flag"},
		{[]string{"workflow", "attach", "brief", "extra"}, "unexpected argument"},
	} {
		t.Run(strings.Join(c.args, "-"), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIWorkflowArgumentErrors$", "--"}, c.args...)...)
			cmd.Env = append(os.Environ(), "HYPERPLANE_TEST_CLI_DISPATCH=1", "TEST_DATABASE_URL=", "DATABASE_URL=")
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("argument mistake did not fail before launch: exit=%v output=%s", err, output)
			}
			if !strings.Contains(string(output), c.want) {
				t.Fatalf("rejection does not say what to type instead (want %q): %s", c.want, output)
			}
		})
	}
}

// The sign-off policy is a promise about what happens when a run finishes, so
// `workflow show` states the consequence rather than echoing the setting word.
func TestWorkflowSignOffSentenceStatesTheConsequence(t *testing.T) {
	for policy, want := range map[string]string{
		store.SignOffRequired:     "waits for your decision",
		store.SignOffOptional:     "never queue",
		store.SignOffAutoOnChecks: "other endings still ask you",
		"":                        "waits for your decision",
	} {
		if got := signOffSentence(policy); !strings.Contains(got, want) {
			t.Errorf("signOffSentence(%q) = %q, want it to mention %q", policy, got, want)
		}
	}
}
