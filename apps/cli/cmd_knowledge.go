// cmd_knowledge.go implements `hyperplane knowledge save <runId>`.
// It exports a saved SDK request, checkpoint and applicable human outcome.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/knowledge"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdKnowledge dispatches the `knowledge` verb.
func cmdKnowledge(args []string, globalNoColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane knowledge save <runId>")
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "save":
		return cmdKnowledgeSave(rest, globalNoColor)
	default:
		fmt.Fprintf(os.Stderr, "knowledge: unknown sub-command %q\n", sub)
		fmt.Fprintln(os.Stderr, "usage: hyperplane knowledge save <runId>")
		return 2
	}
}

// Usage: hyperplane knowledge save <runId> [--target <dir>] [--json]
func cmdKnowledgeSave(args []string, globalNoColor bool) int {
	asJSON := false
	targetRoot := ""
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--target":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || strings.HasPrefix(args[i+1], "--") {
				fmt.Fprintln(os.Stderr, "knowledge save: --target requires a directory")
				return 2
			}
			targetRoot = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "knowledge save: unknown option %q\n", args[i])
				return 2
			}
			positional = append(positional, args[i])
		}
	}

	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane knowledge save <runId> [--target <dir>] [--json]")
		return 2
	}
	runID := positional[0]

	cfg := config.Load()
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	defer st.Close()

	run, err := st.GetRun(ctx, runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knowledge save: run %q not found: %v\n", runID, err)
		return 1
	}

	decision, err := st.DeliveryDecisionForRun(ctx, runID)
	var outcome *store.Decision
	if err == nil {
		outcome = &decision
	} else if !errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "knowledge save: read human outcome: %v\n", err)
		return 1
	}

	// Determine target root: flag > config > cwd.
	if targetRoot == "" {
		targetRoot = cfg.TargetRoot
	}
	if targetRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "knowledge save: getwd: %v\n", err)
			return 1
		}
		targetRoot = cwd
	}

	path, err := knowledge.ExportRun(ctx, targetRoot, run, outcome)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knowledge save: %v\n", err)
		return 1
	}

	type result struct {
		RunID      string `json:"runId"`
		TargetRoot string `json:"targetRoot"`
		Path       string `json:"path"`
		Status     string `json:"status"`
	}
	res := result{
		RunID:      runID,
		TargetRoot: targetRoot,
		Path:       path,
		Status:     "saved",
	}

	if asJSON {
		printJSON(res)
		return 0
	}

	noColor := globalNoColor
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		noColor = true
	}
	fmt.Printf("SDK receipt saved  run=%s  path=%s\n",
		colorize(ansiBold, runID, noColor), path)
	return 0
}
