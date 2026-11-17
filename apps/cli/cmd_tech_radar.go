// cmd_tech_radar.go implements the `tech-radar` verb: lists tech radar entries
// grouped by quadrant → ring, with linked skill IDs.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// cmdTechRadar fetches and displays tech radar entries from the running API server.
// Usage: hyperplane tech-radar list [--json]
func cmdTechRadar(args []string, globalNoColor bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hyperplane tech-radar list [--json]")
		return 2
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "list":
		return cmdTechRadarList(rest, globalNoColor)
	default:
		fmt.Fprintf(os.Stderr, "tech-radar: unknown sub-command %q\n", sub)
		fmt.Fprintln(os.Stderr, "usage: hyperplane tech-radar list [--json]")
		return 2
	}
}

func cmdTechRadarList(args []string, globalNoColor bool) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	cfg := config.Load()
	cl := newAPIClient(cfg.Addr)

	body, status, err := cl.get("/api/tech-radar")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tech-radar: %v\n", err)
		return 1
	}
	if status != 200 {
		fmt.Fprintf(os.Stderr, "tech-radar: server returned %d: %s\n", status, body)
		return 1
	}

	if asJSON {
		var raw json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			printJSON(raw)
		}
		return 0
	}

	var entries []store.TechRadar
	if err := mustUnmarshal(body, &entries); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	noColor := globalNoColor || cl.noColor
	if len(entries) == 0 {
		fmt.Println("no tech radar entries — run: make seed (or: hyperplane seed)")
		return 0
	}

	// Group by quadrant → ring.
	quadrantOrder := []string{"languages", "tools", "techniques", "platforms"}
	ringOrder := []string{"adopt", "trial", "assess", "hold"}

	type group struct {
		quadrant string
		ring     string
		entries  []store.TechRadar
	}

	// Build index.
	byQR := map[string]map[string][]store.TechRadar{}
	for _, e := range entries {
		if byQR[e.Quadrant] == nil {
			byQR[e.Quadrant] = map[string][]store.TechRadar{}
		}
		byQR[e.Quadrant][e.Ring] = append(byQR[e.Quadrant][e.Ring], e)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	first := true
	for _, q := range quadrantOrder {
		rings, ok := byQR[q]
		if !ok {
			continue
		}
		for _, r := range ringOrder {
			ents, ok := rings[r]
			if !ok {
				continue
			}
			if !first {
				fmt.Fprintln(tw)
			}
			first = false
			header := fmt.Sprintf("%s / %s", q, r)
			fmt.Fprintln(tw, colorize(ansiBold, header, noColor))
			fmt.Fprintln(tw, "NAME\tSKILL\tNOTE")
			for _, e := range ents {
				skill := e.SkillRef
				if skill == "" {
					skill = "-"
				}
				note := e.Note
				if len(note) > 60 {
					note = note[:57] + "..."
				}
				if note == "" {
					note = e.Description
					if len(note) > 60 {
						note = note[:57] + "..."
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					colorize(ansiBold, e.Tech, noColor), skill, note)
			}
		}
	}
	// Any quadrants not in the canonical order.
	for q, rings := range byQR {
		known := false
		for _, cq := range quadrantOrder {
			if cq == q {
				known = true
				break
			}
		}
		if known {
			continue
		}
		for _, r := range ringOrder {
			ents, ok := rings[r]
			if !ok {
				continue
			}
			if !first {
				fmt.Fprintln(tw)
			}
			first = false
			header := fmt.Sprintf("%s / %s", q, r)
			fmt.Fprintln(tw, colorize(ansiBold, header, noColor))
			fmt.Fprintln(tw, "NAME\tSKILL\tNOTE")
			for _, e := range ents {
				skill := e.SkillRef
				if skill == "" {
					skill = "-"
				}
				note := e.Note
				if len(note) > 60 {
					note = note[:57] + "..."
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					colorize(ansiBold, e.Tech, noColor), skill, note)
			}
		}
	}
	_ = tw.Flush()
	return 0
}
