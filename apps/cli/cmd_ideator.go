package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/sdkruntime"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdIdeator launches the SDK recipe directly, without graph discovery or parsing.
func cmdIdeator(args []string) int {
	flags := flag.NewFlagSet("ideator", flag.ContinueOnError)
	project := flags.String("project", "", "project label")
	correction := flags.Bool("auto-repair", false, "allow one correction against failed acceptance")
	asJSON := flags.Bool("json", false, "print run ID and state as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" || len(flags.Arg(0)) > 4000 {
		fmt.Fprintln(os.Stderr, `usage: hyperplane ideator [--project name] [--auto-repair] [--json] "Your app brief"`)
		return 2
	}
	brief := strings.TrimSpace(flags.Arg(0))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
	result, err := sdkruntime.RunAppIdeator(ctx, st, sdkruntime.Options{
		Goal: brief, Project: *project, RunsDir: cfg.RunsDir, Inputs: map[string]string{"domain": brief}, AutoRepair: *correction,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"runId": result.RunID, "state": result.State, "workflow": "app-ideator"})
	} else {
		fmt.Printf("%s  %s\n", result.State, result.RunID)
	}
	if result.State != "passed" {
		return 1
	}
	return 0
}
