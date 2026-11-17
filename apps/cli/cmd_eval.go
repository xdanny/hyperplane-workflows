package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/eval"
	"github.com/xdanny/hyperplane-workflows/internal/optimize"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func cmdEval(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane eval <list|run|show|compare|seed|gen>")
		return 1
	}
	sub := args[0]
	rest := args[1:]

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

	switch sub {
	case "list":
		return evalList(ctx, st, rest)
	case "run":
		return evalRun(ctx, st, rest)
	case "show":
		return evalShow(ctx, st, rest)
	case "compare":
		return evalCompare(ctx, st, rest)
	case "seed":
		return evalSeed(ctx, st, rest)
	case "gen":
		return evalGen(ctx, st, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown eval subcommand: %s\n", sub)
		return 1
	}
}

func evalList(ctx context.Context, st *store.Store, args []string) int {
	asJSON := evalHasFlag(args, "--json")
	list, err := st.ListEvalsets(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list evalsets: %v\n", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return 0
	}
	if len(list) == 0 {
		fmt.Println("no evalsets found")
		return 0
	}
	fmt.Printf("%-20s %-30s %-15s %s\n", "ID", "NAME", "SKILL", "CASES")
	for _, e := range list {
		fmt.Printf("%-20s %-30s %-15s %d\n", e.ID, e.Name, e.SkillRef, len(e.Cases))
	}
	return 0
}

func evalRun(ctx context.Context, st *store.Store, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane eval run <evalsetId> [--engine codex] [--model X] [--target skill:<id>] [--judge codex|local|none] [--judge-model name] [--mock] [--json]")
		return 1
	}
	evalsetID := args[0]
	rest := args[1:]

	asJSON := evalHasFlag(rest, "--json")
	useMock := evalHasFlag(rest, "--mock")
	engineName := evalFlagVal(rest, "--engine", "codex")
	model := evalFlagVal(rest, "--model", "")
	target := evalFlagVal(rest, "--target", "")

	var eng eval.Engine
	switch {
	case useMock || engineName == "mock":
		eng = &eval.MockEngine{Responses: map[string]json.RawMessage{
			"": json.RawMessage(`"mock output from hyperplane eval run"`),
		}}
	case engineName == "local" || engineName == "lmstudio" || engineName == "openai-local":
		eng = &eval.LocalEngine{Model: model}
	case engineName == "codex":
		eng = &eval.CodexEngine{Model: model, CWD: "."}
	default:
		fmt.Fprintln(os.Stderr, "eval run: choose engine codex, local or mock")
		return 1
	}
	defaultJudge := "codex"
	switch eng.(type) {
	case *eval.LocalEngine:
		defaultJudge = "local"
	case *eval.MockEngine:
		defaultJudge = "none"
	}
	var judge eval.Judge
	switch evalFlagVal(rest, "--judge", defaultJudge) {
	case "codex":
		judge = &eval.CodexJudge{CWD: ".", Model: evalFlagVal(rest, "--judge-model", "")}
	case "local":
		judge = &eval.LocalJudge{Model: evalFlagVal(rest, "--judge-model", "")}
	case "none":
	default:
		fmt.Fprintln(os.Stderr, "eval run: choose judge codex, local or none")
		return 1
	}

	run, err := eval.Run(ctx, st, eval.Config{
		EvalsetID: evalsetID,
		Target:    target,
		Engine:    eng,
		Judge:     judge,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval run: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(run)
		return 0
	}

	fmt.Printf("Eval Run: %s\n", run.ID)
	fmt.Printf("Evalset:  %s\n", run.EvalsetID)
	fmt.Printf("Status:   %s\n", run.Status)
	fmt.Printf("PassRate: %.1f%%\n", run.PassRate*100)
	fmt.Printf("MeanScore: %.2f\n", run.MeanScore)
	fmt.Printf("\n%-10s %-8s %-8s %s\n", "CASE", "PASSED", "SCORE", "REASONS")
	for _, r := range run.Results {
		pass := "no"
		if r.Passed {
			pass = "yes"
		}
		fmt.Printf("%-10s %-8s %-8.2f %s\n", r.CaseID, pass, r.Score, strings.Join(r.Reasons, "; "))
	}
	return 0
}

func evalShow(ctx context.Context, st *store.Store, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane eval show <runId>")
		return 1
	}
	asJSON := evalHasFlag(args, "--json")
	run, err := st.GetEvalRun(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "get eval run: %v\n", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(run)
		return 0
	}
	fmt.Printf("Run ID:    %s\n", run.ID)
	fmt.Printf("Evalset:   %s\n", run.EvalsetID)
	fmt.Printf("Target:    %s\n", run.Target)
	fmt.Printf("Engine:    %s\n", run.Engine)
	fmt.Printf("Status:    %s\n", run.Status)
	fmt.Printf("Started:   %s\n", run.StartedAt.Format(time.RFC3339))
	fmt.Printf("PassRate:  %.1f%%\n", run.PassRate*100)
	fmt.Printf("MeanScore: %.2f\n", run.MeanScore)
	fmt.Printf("\n%-10s %-8s %-8s %s\n", "CASE", "PASSED", "SCORE", "REASONS")
	for _, r := range run.Results {
		pass := "no"
		if r.Passed {
			pass = "yes"
		}
		fmt.Printf("%-10s %-8s %-8.2f %s\n", r.CaseID, pass, r.Score, strings.Join(r.Reasons, "; "))
	}
	return 0
}

func evalCompare(ctx context.Context, st *store.Store, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane eval compare <runIdA> <runIdB>")
		return 1
	}
	asJSON := evalHasFlag(args, "--json")
	runA, err := st.GetEvalRun(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "get run A: %v\n", err)
		return 1
	}
	runB, err := st.GetEvalRun(ctx, args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "get run B: %v\n", err)
		return 1
	}
	result, err := eval.CompareRuns(runA, runB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compare: %v\n", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
		return 0
	}
	fmt.Printf("Compare: %s vs %s\n", result.RunAID, result.RunBID)
	fmt.Printf("Winner:  %s\n", result.Winner)
	fmt.Printf("PassRate diff:  %+.1f%%\n", result.PassRateDiff*100)
	fmt.Printf("MeanScore diff: %+.2f\n", result.MeanScoreDiff)
	fmt.Printf("\n%-10s %-10s %-10s %-8s %-8s\n", "CASE", "PASSED_A", "PASSED_B", "SCORE_A", "SCORE_B")
	for _, c := range result.PerCase {
		pa, pb := "no", "no"
		if c.PassedA {
			pa = "yes"
		}
		if c.PassedB {
			pb = "yes"
		}
		fmt.Printf("%-10s %-10s %-10s %-8.2f %-8.2f\n", c.CaseID, pa, pb, c.ScoreA, c.ScoreB)
	}
	return 0
}

func evalSeed(ctx context.Context, st *store.Store, args []string) int {
	dir := evalFlagVal(args, "--from", "evals")
	n, err := eval.SeedEvalsets(ctx, st, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		return 1
	}
	fmt.Printf("seeded %d evalset(s) from %s\n", n, dir)
	return 0
}

// evalHasFlag checks for a boolean flag in args (prefixed to avoid collisions).
func evalHasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// evalFlagVal reads a --key value flag from args, returning def if not found.
func evalFlagVal(args []string, flag, def string) string {
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

// evalGen implements `hyperplane eval gen`.
//
// Single-skill mode:
//
//	eval gen <skillId> [--engine codex|local] [--json]
//
// Batch mode:
//
//	eval gen --all [--engine codex|local] [--only-missing] [--max N] [--json]
func evalGen(ctx context.Context, st *store.Store, args []string) int {
	asJSON := evalHasFlag(args, "--json")
	all := evalHasFlag(args, "--all")
	onlyMissing := evalHasFlag(args, "--only-missing")
	engineName := evalFlagVal(args, "--engine", "local")
	maxStr := evalFlagVal(args, "--max", "0")
	maxN, _ := strconv.Atoi(maxStr)

	if all {
		return evalGenAll(ctx, st, engineName, onlyMissing, maxN, asJSON)
	}

	// Single-skill mode: first positional arg is the skill id.
	var skillID string
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			skillID = a
			break
		}
	}
	if skillID == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane eval gen <skillId> [--engine codex|local] [--json]")
		fmt.Fprintln(os.Stderr, "       hyperplane eval gen --all [--engine codex|local] [--only-missing] [--max N] [--json]")
		return 1
	}
	return evalGenOne(ctx, st, skillID, engineName, asJSON)
}

// evalGenOne generates and persists an evalset for a single skill.
func evalGenOne(ctx context.Context, st *store.Store, skillID, engineName string, asJSON bool) int {
	es, err := optimize.GenerateEvalset(ctx, st, skillID, engineName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval gen %s: %v\n", skillID, err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(es)
		return 0
	}
	fmt.Printf("evalset: %s\n", es.ID)
	fmt.Printf("skill:   %s\n", es.SkillRef)
	fmt.Printf("cases:   %d\n\n", len(es.Cases))
	for i, c := range es.Cases {
		fmt.Printf("  [%d] %s\n      criteria: %s\n", i+1, c.ID, c.Criteria)
	}
	return 0
}

// evalGenAll generates evalsets for all skills (or only those missing one).
func evalGenAll(ctx context.Context, st *store.Store, engineName string, onlyMissing bool, maxN int, asJSON bool) int {
	skills, err := st.ListSkills(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list skills: %v\n", err)
		return 1
	}

	// Build a set of skill_refs that already have an evalset.
	var hasMissing bool
	coveredSkills := map[string]bool{}
	if onlyMissing {
		evalsets, lerr := st.ListEvalsets(ctx)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "list evalsets: %v\n", lerr)
			return 1
		}
		for _, es := range evalsets {
			if es.SkillRef != "" {
				coveredSkills[es.SkillRef] = true
			}
		}
		hasMissing = true
		_ = hasMissing
	}

	type genResult struct {
		SkillID string `json:"skill_id"`
		Cases   int    `json:"cases,omitempty"`
		Skipped bool   `json:"skipped,omitempty"`
		Error   string `json:"error,omitempty"`
	}

	var results []genResult
	processed := 0
	exitCode := 0

	for _, sk := range skills {
		if maxN > 0 && processed >= maxN {
			break
		}
		if onlyMissing && coveredSkills[sk.ID] {
			results = append(results, genResult{SkillID: sk.ID, Skipped: true})
			continue
		}

		es, genErr := optimize.GenerateEvalset(ctx, st, sk.ID, engineName)
		processed++
		if genErr != nil {
			fmt.Fprintf(os.Stderr, "eval gen %s: %v\n", sk.ID, genErr)
			results = append(results, genResult{SkillID: sk.ID, Error: genErr.Error()})
			exitCode = 1
			continue
		}
		results = append(results, genResult{SkillID: sk.ID, Cases: len(es.Cases)})
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return exitCode
	}

	fmt.Printf("%-30s %-8s %s\n", "SKILL", "CASES", "STATUS")
	for _, r := range results {
		status := "ok"
		cases := strconv.Itoa(r.Cases)
		if r.Skipped {
			status = "skipped (has evalset)"
			cases = "-"
		} else if r.Error != "" {
			status = "error: " + r.Error
			cases = "-"
		}
		fmt.Printf("%-30s %-8s %s\n", r.SkillID, cases, status)
	}
	return exitCode
}
