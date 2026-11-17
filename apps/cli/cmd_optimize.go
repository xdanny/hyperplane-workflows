package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func cmdOptimize(ctx context.Context, args []string) int {
	if len(args) == 0 {
		printOptimizeUsage()
		return 1
	}

	// Subcommand: "list" lists past optimize runs for a skill.
	if args[0] == "list" {
		return optimizeList(ctx, args[1:])
	}

	if args[0] == "node" || args[0] == "nodes" {
		fmt.Fprintln(os.Stderr, "graph node optimization is retired; provide a skill ID")
		return 2
	}

	skillID := args[0]
	rest := args[1:]

	evalsetID := optFlagVal(rest, "--evalset", "")
	variantsN := optFlagInt(rest, "--variants", "4")
	proposer := optFlagVal(rest, "--proposer", "local")
	scoreEngine := optFlagVal(rest, "--score-engine", "local")
	minGainStr := optFlagVal(rest, "--min-gain", "0")
	apply := optHasFlag(rest, "--apply")
	asJSON := optHasFlag(rest, "--json")
	fromRuns := optHasFlag(rest, "--from-runs")

	minGain, err := strconv.ParseFloat(minGainStr, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --min-gain value %q: %v\n", minGainStr, err)
		return 1
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

	if !asJSON {
		fmt.Printf("Optimizing skill: %s\n", skillID)
		if evalsetID != "" {
			fmt.Printf("Evalset:         %s\n", evalsetID)
		}
		fmt.Printf("Proposer:        %s\n", proposer)
		fmt.Printf("Score engine:    %s\n", scoreEngine)
		fmt.Printf("Variants:        %d\n", variantsN)
		fmt.Printf("Min gain:        %.4f\n", minGain)
		fmt.Printf("Apply:           %v\n", apply)
		fmt.Printf("From runs:       %v\n", fromRuns)
		fmt.Println()
	}

	result, err := optimize.Optimize(ctx, st, optimize.Config{
		SkillID:     skillID,
		EvalsetID:   evalsetID,
		Variants:    variantsN,
		Proposer:    proposer,
		ScoreEngine: scoreEngine,
		MinGain:     minGain,
		Apply:       apply,
		FromRuns:    fromRuns,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "optimize: %v\n", err)
		// The flag hint belongs to the terminal, not to the error: the same
		// error reaches the API and the Skill Lab, where "--evalset" is advice
		// nobody can take.
		if errors.Is(err, optimize.ErrNoEvalset) {
			fmt.Fprintf(os.Stderr, "  pass --evalset <id> to score against an existing set, or create one for this skill\n")
		}
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		return 0
	}

	// Pretty-print the results table.
	printOptimizeResult(result)
	return 0
}

func printOptimizeResult(result optimize.Result) {
	fmt.Printf("Skill:   %s\n", result.SkillID)
	fmt.Printf("Evalset: %s\n", result.EvalsetID)
	if result.FeedbackGrounded && result.HarvestSummary != nil {
		s := result.HarvestSummary
		fmt.Printf("Harvest: %d items  (sdk=%d  human-outcome=%d  selection=%d)\n",
			s.Total, s.SDK, s.HumanOutcome, s.Selection)
		fmt.Println("         Associated feedback informs proposals; only configured cases are scored.")
	}
	fmt.Println()

	// Header
	fmt.Printf("%-14s  %-8s  %-8s  %-10s  %s\n",
		"LABEL", "PASS%", "MEAN", "vs BASE", "INSTRUCTION (first 60 chars)")
	fmt.Println(strings.Repeat("-", 90))

	printVariantRow("baseline", result.Baseline, 0, false)
	for _, v := range result.Variants {
		gain := v.PassRate - result.Baseline.PassRate
		isWinner := result.Winner != nil && v.Index == result.Winner.Index
		printVariantRow(v.Label, v, gain, isWinner)
	}

	fmt.Println()
	if result.Winner != nil {
		fmt.Printf("Winner:  %s  (+%.1f%% pass rate)\n",
			result.Winner.Label, result.Gain*100)
		if result.Applied {
			fmt.Println("Applied: instruction updated in skill store")
		} else {
			fmt.Println("(dry run — pass --apply to persist the winner)")
		}
	} else {
		fmt.Println("No variant improved on the baseline — skill instruction unchanged.")
	}

	if result.RunID != "" {
		fmt.Printf("Run ID:  %s\n", result.RunID)
	}
}

func printVariantRow(label string, v optimize.VariantScore, gain float64, winner bool) {
	gainStr := ""
	if v.Index > 0 {
		gainStr = fmt.Sprintf("%+.1f%%", gain*100)
	}
	winMark := ""
	if winner {
		winMark = " *"
	}
	short := v.Instruction
	if len(short) > 60 {
		short = short[:57] + "..."
	}
	// Replace newlines for readability in table.
	short = strings.ReplaceAll(short, "\n", " ")
	fmt.Printf("%-14s  %6.1f%%  %6.2f  %-10s  %s%s\n",
		label, v.PassRate*100, v.MeanScore, gainStr, short, winMark)
}

func optimizeList(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane optimize list <skillId> [--json]")
		return 1
	}
	skillID := args[0]
	asJSON := optHasFlag(args[1:], "--json")

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

	runs, err := optimize.ListOptimizeRuns(ctx, st, skillID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list optimize runs: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if runs == nil {
			_ = enc.Encode([]optimize.OptimizeRun{})
		} else {
			_ = enc.Encode(runs)
		}
		return 0
	}

	if len(runs) == 0 {
		fmt.Printf("no optimize runs found for skill %q\n", skillID)
		return 0
	}
	fmt.Printf("%-26s  %-8s  %-8s  %-6s  %s\n",
		"ID", "BASE%", "GAIN", "APPLIED", "CREATED")
	fmt.Println(strings.Repeat("-", 70))
	for _, r := range runs {
		app := "no"
		if r.Applied {
			app = "yes"
		}
		fmt.Printf("%-26s  %6.1f%%  %+6.1f%%  %-6s  %s\n",
			r.ID, r.BaselinePassRate*100, r.Gain*100, app,
			r.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return 0
}

// printOptimizeUsage describes skill optimization.
func printOptimizeUsage() {
	fmt.Fprintln(os.Stderr, "usage: hyperplane optimize <skillId> [--evalset <id>] [--variants N]")
	fmt.Fprintln(os.Stderr, "                           [--proposer local|codex] [--score-engine local|codex]")
	fmt.Fprintln(os.Stderr, "                           [--min-gain <f>] [--apply] [--from-runs] [--json]")
	fmt.Fprintln(os.Stderr, "       hyperplane optimize list <skillId> [--json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  --from-runs  Require associated SDK or skill-preference context for proposals; score only configured cases.")
	fmt.Fprintln(os.Stderr, "  --apply      Persist the winning instruction in the skill store.")
}

// optHasFlag checks for a boolean flag.
func optHasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// optFlagVal reads a --key value flag.
func optFlagVal(args []string, flag, def string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"=")
		}
	}
	return def
}

// optFlagInt reads an integer flag. defStr is the string representation of the default.
func optFlagInt(args []string, flag, defStr string) int {
	s := optFlagVal(args, flag, defStr)
	n, err := strconv.Atoi(s)
	if err != nil {
		d, _ := strconv.Atoi(defStr)
		return d
	}
	return n
}
