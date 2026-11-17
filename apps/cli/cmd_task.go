package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A project task needs neither a backlog file nor a composed graph.
func cmdTask(args []string) int {
	flags := flag.NewFlagSet("task", flag.ContinueOnError)
	target := flags.String("target", "", "existing project directory")
	checkJSON := flags.String("check", "", "acceptance command as a JSON argument array")
	project := flags.String("project", "", "project label")
	correction := flags.Bool("auto-repair", false, "allow one correction against failed acceptance")
	asJSON := flags.Bool("json", false, "print run ID and state as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	var check []string
	checkSupplied := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "check" {
			checkSupplied = true
		}
	})
	invalidCheck := checkSupplied && (json.Unmarshal([]byte(*checkJSON), &check) != nil || check == nil)
	if flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" || *target == "" || invalidCheck {
		fmt.Fprintln(os.Stderr, `usage: hyperplane task --target <dir> [--check '["program","arg"]'] [--auto-repair] [--project name] [--json] "Goal"`)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := sdkworkflow.Validate(ctx, flags.Arg(0), []sdkworkflow.Node{{ID: "task", Kind: "task", Title: "Project task", Instruction: flags.Arg(0), Check: check}}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, sdkworkflow.ErrValidationUnavailable) {
			return 1
		}
		return 2
	}
	cfg := config.Load()
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	result, err := sdkruntime.RunCheckedTask(ctx, st, sdkruntime.Options{
		TargetRoot: *target, RunsDir: cfg.RunsDir, Goal: flags.Arg(0), Project: *project, AutoRepair: *correction,
	}, flags.Arg(0), check)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"runId": result.RunID, "state": result.State})
	} else {
		fmt.Printf("%s  %s\n", result.State, result.RunID)
	}
	if result.State != "passed" {
		return 1
	}
	return 0
}
