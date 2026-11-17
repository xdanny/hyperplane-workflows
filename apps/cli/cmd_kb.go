// cmd_kb.go implements the `kb` verb: knowledge base sub-commands.
// kb search <query> [--kinds skill,radar,memory] [--limit 20] [--json]
// kb memory list | kb memory set <key> <json> | kb memory delete <key>
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdKB dispatches the `kb` verb.
func cmdKB(args []string, globalNoColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane kb <sub-command>")
		fmt.Fprintln(os.Stderr, "  search <query> [--kinds skill,radar,memory,decision] [--limit N] [--json]")
		fmt.Fprintln(os.Stderr, "  memory list [--kind <k>] [--limit N]")
		fmt.Fprintln(os.Stderr, "  memory set <key> <json>")
		fmt.Fprintln(os.Stderr, "  memory delete <key>")
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "search":
		return cmdKBSearch(rest, globalNoColor)
	case "memory":
		return cmdKBMemory(rest, globalNoColor)
	default:
		fmt.Fprintf(os.Stderr, "kb: unknown sub-command %q\n", sub)
		fmt.Fprintln(os.Stderr, "usage: hyperplane kb search|memory")
		return 2
	}
}

// cmdKBSearch performs a unified search via GET /api/kb.
// Usage: hyperplane kb search <query> [--kinds skill,radar,memory,decision] [--limit N] [--json]
func cmdKBSearch(args []string, globalNoColor bool) int {
	asJSON := false
	kinds := ""
	limit := 20
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--kinds":
			if i+1 < len(args) {
				kinds = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		default:
			positional = append(positional, args[i])
		}
	}

	query := strings.Join(positional, " ")

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if kinds != "" {
		q.Set("kinds", kinds)
	}
	q.Set("limit", fmt.Sprintf("%d", limit))

	path := "/api/kb?" + q.Encode()
	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kb search: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "kb search: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	var results []store.KBResult
	if err := mustUnmarshal(body, &results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor || cl.noColor
	if len(results) == 0 {
		fmt.Println("no results")
		return 0
	}

	// Group by kind for TTY output.
	byKind := map[string][]store.KBResult{}
	kindOrder := []string{}
	for _, r := range results {
		if _, seen := byKind[r.Kind]; !seen {
			kindOrder = append(kindOrder, r.Kind)
		}
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}

	for _, kind := range kindOrder {
		items := byKind[kind]
		fmt.Printf("\n%s\n", colorize(ansiBold, strings.ToUpper(kind), noColor))
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, colorize(ansiBold, "ID\tSCORE\tSUMMARY", noColor))
		for _, r := range items {
			fmt.Fprintf(tw, "%s\t%.2f\t%s\n", r.ID, r.Score, r.Summary)
		}
		_ = tw.Flush()
	}
	return 0
}

// cmdKBMemory dispatches `kb memory` sub-commands.
func cmdKBMemory(args []string, globalNoColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane kb memory list|set|delete")
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "list":
		return cmdKBMemoryList(rest, globalNoColor)
	case "set":
		return cmdKBMemorySet(rest, globalNoColor)
	case "delete":
		return cmdKBMemoryDelete(rest, globalNoColor)
	default:
		fmt.Fprintf(os.Stderr, "kb memory: unknown sub-command %q\n", sub)
		fmt.Fprintln(os.Stderr, "usage: hyperplane kb memory list|set|delete")
		return 2
	}
}

// cmdKBMemoryList lists memory items via GET /api/kb/memory.
func cmdKBMemoryList(args []string, globalNoColor bool) int {
	asJSON := false
	kind := ""
	limit := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	path := "/api/kb/memory"
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	body, status, err := cl.get(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kb memory list: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "kb memory list: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	var items []store.MemoryItem
	if err := mustUnmarshal(body, &items); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor || cl.noColor
	if len(items) == 0 {
		fmt.Println("no memory items")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, colorize(ansiBold, "KEY\tKIND\tSOURCE\tUPDATED\tVALUE", noColor))
	for _, m := range items {
		age := time.Since(m.UpdatedAt).Truncate(time.Minute)
		val := compactValue(m.Value, 60)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s ago\t%s\n",
			colorize(ansiBold, m.Key, noColor), m.Kind, m.Source, age, val)
	}
	_ = tw.Flush()
	return 0
}

// cmdKBMemorySet upserts a memory item via POST /api/kb/memory.
// Usage: hyperplane kb memory set <key> <json>
// The json argument is a raw JSON object for the value field; kind defaults to
// "user-pref" unless a --kind flag is passed.
func cmdKBMemorySet(args []string, globalNoColor bool) int {
	asJSON := false
	kind := "user-pref"
	source := "human"

	// Parse flags; positional args are key + json.
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--source":
			if i+1 < len(args) {
				source = args[i+1]
				i++
			}
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane kb memory set <key> <json> [--kind <k>] [--source <s>]")
		return 2
	}
	key := positional[0]
	rawValue := positional[1]

	// Validate that the value is valid JSON.
	var valueCheck json.RawMessage
	if err := json.Unmarshal([]byte(rawValue), &valueCheck); err != nil {
		// If not a JSON object, wrap it as {"v": rawValue} to still be valid JSON.
		wrapped, _ := json.Marshal(map[string]string{"v": rawValue})
		valueCheck = wrapped
	}

	item := store.MemoryItem{
		Kind:   kind,
		Key:    key,
		Value:  valueCheck,
		Source: source,
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.postJSON("/api/kb/memory", item)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kb memory set: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "kb memory set: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}
	fmt.Printf("memory set: %s (%s)\n", key, kind)
	return 0
}

// cmdKBMemoryDelete removes a memory item via DELETE /api/kb/memory/{key}.
func cmdKBMemoryDelete(args []string, globalNoColor bool) int {
	asJSON := false
	var positional []string
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else {
			positional = append(positional, a)
		}
	}

	if len(positional) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane kb memory delete <key>")
		return 2
	}
	key := positional[0]

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.deleteReq("/api/kb/memory/" + url.PathEscape(key))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kb memory delete: %v\n", err)
		return 1
	}
	if status == 404 {
		fmt.Fprintf(os.Stderr, "kb memory delete: key %q not found\n", key)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "kb memory delete: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}
	fmt.Printf("memory deleted: %s\n", key)
	return 0
}

// compactValue renders a JSON value as a compact string clipped to maxChars.
func compactValue(v json.RawMessage, maxChars int) string {
	if len(v) == 0 {
		return "{}"
	}
	var raw any
	if err := json.Unmarshal(v, &raw); err != nil {
		s := string(v)
		if len(s) > maxChars {
			return s[:maxChars] + "…"
		}
		return s
	}
	b, err := json.Marshal(raw)
	if err != nil {
		s := string(v)
		if len(s) > maxChars {
			return s[:maxChars] + "…"
		}
		return s
	}
	s := string(b)
	if len(s) > maxChars {
		return s[:maxChars] + "…"
	}
	return s
}
