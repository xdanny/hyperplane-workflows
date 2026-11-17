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
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func cmdRoute(ctx context.Context, args []string, noColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane route <eval|list|set|clear>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  route eval --skill <id> --evalset <evalsetId> [--margin 0.1] [--json]")
		fmt.Fprintln(os.Stderr, "       Run evalset twice (local + codex), compare, upsert routing policy.")
		fmt.Fprintln(os.Stderr, "  route list [--json]")
		fmt.Fprintln(os.Stderr, "       List all routing policies.")
		fmt.Fprintln(os.Stderr, "  route set --scope <s> --engine <e> [--note <text>]")
		fmt.Fprintln(os.Stderr, "       Manually upsert a routing policy.")
		fmt.Fprintln(os.Stderr, "  route clear --scope <s>")
		fmt.Fprintln(os.Stderr, "       Remove a routing policy.")
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
	case "eval":
		return routeEval(ctx, st, rest, cfg)
	case "list":
		return routeList(ctx, st, rest)
	case "set":
		return routeSet(ctx, st, rest)
	case "clear":
		return routeClear(ctx, st, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown route subcommand: %s\n", sub)
		return 1
	}
}

// routeEval runs the evalset twice (once with local, once with codex), compares
// results, and upserts the routing policy for the skill.
func routeEval(ctx context.Context, st *store.Store, args []string, cfg config.Config) int {
	skillID := routeFlagVal(args, "--skill", "")
	evalsetID := routeFlagVal(args, "--evalset", "")
	marginStr := routeFlagVal(args, "--margin", "0.1")
	asJSON := routeHasFlag(args, "--json")

	if skillID == "" || evalsetID == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane route eval --skill <id> --evalset <evalsetId> [--margin 0.1]")
		return 1
	}

	margin, err := strconv.ParseFloat(marginStr, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --margin value %q: %v\n", marginStr, err)
		return 1
	}

	result, err := eval.EvaluateRouting(ctx, st, skillID, evalsetID, margin, cfg.TargetRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "routing eval: %v\n", err)
		return 1
	}
	if asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Printf("Comparison: pass-rate difference=%+.1f%% winner=%s\n", result.Compare.PassRateDiff*100, result.Compare.Winner)
	fmt.Printf("Decision: skill:%s → engine=%s\n", skillID, result.Decision)
	fmt.Printf("Policy saved with evaluation receipts %s and %s\n", result.Policy.EvalRunA, result.Policy.EvalRunB)
	return 0
}

// routeList prints all routing policies.
func routeList(ctx context.Context, st *store.Store, args []string) int {
	asJSON := routeHasFlag(args, "--json")

	list, err := st.ListRoutingPolicies(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list routing policies: %v\n", err)
		return 1
	}

	if asJSON {
		if list == nil {
			list = []store.RoutingPolicy{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return 0
	}

	if len(list) == 0 {
		fmt.Println("no routing policies found")
		return 0
	}

	fmt.Printf("%-30s %-12s %-10s %s\n", "SCOPE", "ENGINE", "CONFIDENCE", "UPDATED")
	for _, p := range list {
		conf := "-"
		if p.Confidence != nil {
			conf = fmt.Sprintf("%.2f", *p.Confidence)
		}
		fmt.Printf("%-30s %-12s %-10s %s\n",
			p.Scope, p.Engine, conf,
			p.UpdatedAt.Format(time.RFC3339))
	}
	return 0
}

// routeSet manually upserts a routing policy.
func routeSet(ctx context.Context, st *store.Store, args []string) int {
	scope := routeFlagVal(args, "--scope", "")
	engine := routeFlagVal(args, "--engine", "")
	note := routeFlagVal(args, "--note", "")

	if scope == "" || engine == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane route set --scope <s> --engine <e> [--note <text>]")
		return 1
	}

	p := store.RoutingPolicy{
		Scope:  scope,
		Engine: engine,
		Note:   note,
	}
	if err := st.UpsertRoutingPolicy(ctx, p); err != nil {
		fmt.Fprintf(os.Stderr, "upsert routing policy: %v\n", err)
		return 1
	}
	fmt.Printf("routing policy set: %s → %s\n", scope, engine)
	return 0
}

// routeClear removes a routing policy.
func routeClear(ctx context.Context, st *store.Store, args []string) int {
	scope := routeFlagVal(args, "--scope", "")
	if scope == "" {
		fmt.Fprintln(os.Stderr, "usage: hyperplane route clear --scope <s>")
		return 1
	}
	if err := st.DeleteRoutingPolicy(ctx, scope); err != nil {
		fmt.Fprintf(os.Stderr, "delete routing policy %q: %v\n", scope, err)
		return 1
	}
	fmt.Printf("routing policy cleared: %s\n", scope)
	return 0
}

// ── flag helpers ─────────────────────────────────────────────────────────────

func routeHasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func routeFlagVal(args []string, flag, def string) string {
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
