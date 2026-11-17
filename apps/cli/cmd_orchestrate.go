package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/orchestrate"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdOrchestrate runs the self-driving backlog orchestrator.
// It reads a backlog JSON file and runs one checked Codex SDK session for each item.
func cmdOrchestrate(ctx context.Context, args []string, noColor bool) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	backlogFile := ""
	maxItems := 0
	engineName := "codex-sdk"
	targetDir := ""
	dryRun := false
	stopOnFail := false
	asJSON := false
	autoRepair := false
	project := ""

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--backlog" && i+1 < len(args):
			backlogFile = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--backlog="):
			backlogFile = strings.TrimPrefix(args[i], "--backlog=")
		case args[i] == "--max" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &maxItems)
			i++
		case strings.HasPrefix(args[i], "--max="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--max="), "%d", &maxItems)
		case args[i] == "--engine" && i+1 < len(args):
			engineName = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--engine="):
			engineName = strings.TrimPrefix(args[i], "--engine=")
		case args[i] == "--target" && i+1 < len(args):
			targetDir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--target="):
			targetDir = strings.TrimPrefix(args[i], "--target=")
		case args[i] == "--auto-repair":
			autoRepair = true
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case args[i] == "--dry-run":
			dryRun = true
		case args[i] == "--stop-on-fail":
			stopOnFail = true
		case args[i] == "--json":
			asJSON = true
		}
	}

	if backlogFile == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane orchestrate --backlog <file.json> [--max N]")
		fmt.Fprintln(os.Stderr, "         [--engine codex-sdk] [--target <dir>] [--dry-run]")
		fmt.Fprintln(os.Stderr, "         [--stop-on-fail] [--auto-repair] [--project name] [--json]")
		return 2
	}

	items, err := orchestrate.LoadBacklog(backlogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orchestrate: load backlog: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "orchestrate: backlog is empty")
		return 1
	}

	cfg := config.Load()
	if targetDir == "" {
		targetDir = cfg.TargetRoot
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		return 1
	}

	if !asJSON {
		fmt.Printf("Backlog:    %d item(s)\n", len(items))
		fmt.Printf("Max items:  %d\n", effectiveMax(maxItems, len(items)))
		fmt.Printf("Engine:     %s\n", engineName)
		fmt.Printf("Target:     %s\n", targetDir)
		fmt.Printf("Dry-run:    %v\n", dryRun)
		fmt.Printf("StopOnFail: %v\n", stopOnFail)
		fmt.Println()
	}

	result, err := orchestrate.Orchestrate(ctx, st, orchestrate.OrchestrateConfig{
		BacklogItems: items,
		MaxItems:     maxItems,
		TargetRoot:   targetDir,
		Engine:       engineName,
		DryRun:       dryRun,
		StopOnFail:   stopOnFail,
		RunsDir:      cfg.RunsDir,
		AutoRepair:   autoRepair,
		Project:      project,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "orchestrate: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		if result.Failed > 0 {
			return 1
		}
		return 0
	}

	// Pretty table.
	fmt.Printf("%-40s  %-10s  %s\n", "TITLE", "STATE", "RUN ID")
	fmt.Println(strings.Repeat("-", 80))
	for _, it := range result.Items {
		title := it.Title
		if len(title) > 38 {
			title = title[:35] + "..."
		}
		stateStr := itemStateColor(it.State, noColor)
		runID := it.RunID
		if runID == "" {
			runID = "-"
		}
		fmt.Printf("%-40s  %-10s  %s\n", title, stateStr, runID)
	}
	fmt.Println()
	fmt.Printf("done=%d  failed=%d  total=%d\n", result.Done, result.Failed, len(result.Items))

	if result.Failed > 0 {
		return 1
	}
	return 0
}

func itemStateColor(state string, noColor bool) string {
	switch state {
	case "done":
		return colorize(ansiGreen, state, noColor)
	case "failed":
		return colorize(ansiRed, state, noColor)
	case "dry-run":
		return colorize(ansiCyan, state, noColor)
	default:
		return state
	}
}

func effectiveMax(max, total int) int {
	if max <= 0 || max > total {
		return total
	}
	return max
}
