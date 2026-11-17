package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/improve"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdImprove runs one self-improvement pass (always --once; no daemon mode).
//
// Usage:
//
//	hyperplane improve [--once] [--skill <id>] [--max N] [--min-feedback K]
//	                   [--apply] [--engine e] [--dry-run] [--json]
//
// --once is the default and only mode (the flag is accepted for clarity but
// does not change behaviour — continuous scheduling is handled externally).
func cmdImprove(ctx context.Context, args []string, noColor bool) int {
	var skills []string
	maxSkills := 0
	minFeedback := 3
	apply := false
	engine := ""
	dryRun := false
	asJSON := false
	// --once is accepted but is a no-op (single pass is the only mode).

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--once":
			// single pass is already the default; accept and ignore.
		case args[i] == "--skill" && i+1 < len(args):
			skills = append(skills, args[i+1])
			i++
		case strings.HasPrefix(args[i], "--skill="):
			skills = append(skills, strings.TrimPrefix(args[i], "--skill="))
		case args[i] == "--max" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &maxSkills)
			i++
		case strings.HasPrefix(args[i], "--max="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--max="), "%d", &maxSkills)
		case args[i] == "--min-feedback" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &minFeedback)
			i++
		case strings.HasPrefix(args[i], "--min-feedback="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--min-feedback="), "%d", &minFeedback)
		case args[i] == "--engine" && i+1 < len(args):
			engine = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--engine="):
			engine = strings.TrimPrefix(args[i], "--engine=")
		case args[i] == "--apply":
			apply = true
		case args[i] == "--dry-run":
			dryRun = true
		case args[i] == "--json":
			asJSON = true
		}
	}

	cfg := config.Load()

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

	// Create the improve_run record so it is immediately queryable.
	runID := time.Now().Format("20060102-150405.000") + "-improve"
	initialRun := store.ImproveRun{
		ID:     runID,
		Status: "running",
	}
	if _, err := st.CreateImproveRun(ctx, initialRun); err != nil {
		fmt.Fprintf(os.Stderr, "create improve run: %v\n", err)
		return 1
	}

	if !asJSON {
		fmt.Printf("Improve run: %s\n", runID)
		fmt.Printf("Skills:      %s\n", skillsLabel(skills))
		fmt.Printf("MaxSkills:   %d\n", maxSkills)
		fmt.Printf("MinFeedback: %d\n", minFeedback)
		fmt.Printf("Apply:       %v\n", apply)
		fmt.Printf("Engine:      %s\n", engineLabel(engine))
		fmt.Printf("DryRun:      %v\n", dryRun)
		fmt.Printf("Mode:        once (single pass; no daemon)\n")
		fmt.Println()
	}

	result, err := improve.Improve(ctx, st, improve.ImproveConfig{
		Skills:      skills,
		MinFeedback: minFeedback,
		MaxSkills:   maxSkills,
		Apply:       apply,
		Engine:      engine,
		DryRun:      dryRun,
	})

	// Persist the final state whether or not there was an error.
	status := "done"
	if err != nil {
		status = "failed"
	}
	itemsJSON, _ := json.Marshal(result.Items)
	finalRun := store.ImproveRun{
		ID:               runID,
		SkillsConsidered: result.SkillsConsidered,
		SkillsOptimized:  result.SkillsOptimized,
		SkillsPromoted:   result.SkillsPromoted,
		Items:            itemsJSON,
		Status:           status,
	}
	if updateErr := st.UpdateImproveRun(ctx, finalRun); updateErr != nil {
		fmt.Fprintf(os.Stderr, "update improve run: %v\n", updateErr)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "improve: %v\n", err)
		return 1
	}

	if asJSON {
		out := map[string]any{
			"id":     runID,
			"result": result,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}

	// Pretty-print per-skill outcomes.
	fmt.Printf("%-36s  %s\n", "SKILL", "STATUS")
	fmt.Println(strings.Repeat("-", 70))
	for _, it := range result.Items {
		suffix := ""
		if it.Winner != "" {
			suffix = "  (winner: " + it.Winner + ")"
		}
		statusStr := improveStatusColor(it.Status, noColor)
		fmt.Printf("%-36s  %s%s\n", it.SkillRef, statusStr, suffix)
	}
	fmt.Println()
	// The four numbers add up: considered = optimized + skipped + failed.
	fmt.Printf("considered=%d  optimized=%d  skipped=%d  failed=%d  promoted=%d\n",
		result.SkillsConsidered, result.SkillsOptimized, result.SkillsSkipped,
		result.SkillsFailed, result.SkillsPromoted)
	fmt.Printf("Run ID: %s\n", runID)

	return 0
}

func skillsLabel(skills []string) string {
	if len(skills) == 0 {
		return "(all with sufficient feedback)"
	}
	return strings.Join(skills, ", ")
}

func engineLabel(e string) string {
	if e == "" {
		return "local (default)"
	}
	return e
}

func improveStatusColor(status string, noColor bool) string {
	switch {
	case status == "promoted":
		return colorize(ansiGreen, status, noColor)
	case strings.HasPrefix(status, "error"):
		return colorize(ansiRed, status, noColor)
	case strings.HasPrefix(status, "skipped"):
		return colorize(ansiCyan, status, noColor)
	default:
		return status
	}
}
