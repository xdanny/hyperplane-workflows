package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// KBQuery is the unified search request over the knowledge base.
type KBQuery struct {
	Q     string   // free-text query (empty = return recent items)
	Kinds []string // filter: "skill" | "memory" | "radar" | "decision"
	Limit int      // max results (default 20)
}

// KBResult is a single entry returned by SearchKB.
type KBResult struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Score   float64         `json:"score"`
	Summary string          `json:"summary"`
	Payload json.RawMessage `json:"payload"`
}

// kindEnabled reports whether the given kind is requested.
// When q.Kinds is empty, all kinds are enabled.
func (q KBQuery) kindEnabled(kind string) bool {
	if len(q.Kinds) == 0 {
		return true
	}
	for _, k := range q.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// recencyScore returns a score in [0, 1] based on how recent t is.
// Items created within the last 24 h score 1.0; items older than 30 days score 0.0.
const recencyWindowDays = 30

func recencyScore(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	age := time.Since(t)
	if age < 0 {
		age = 0
	}
	days := age.Hours() / 24.0
	if days >= recencyWindowDays {
		return 0
	}
	return 1.0 - (days / recencyWindowDays)
}

// combinedScore computes: 0.7*trgmSim + 0.3*recency
// When trgmSim is 0 (no query), recency dominates.
func combinedScore(trgmSim float64, createdAt time.Time) float64 {
	return 0.7*trgmSim + 0.3*recencyScore(createdAt)
}

// SearchKB performs a unified fan-out search over skills, memory, decisions,
// and tech_radar. Results are ranked by 0.7*trgm_sim + 0.3*recency and capped
// at q.Limit (default 20).
//
// Each source is queried independently; the results are merged and sorted.
// Kinds filter (q.Kinds) controls which sources participate.
func (s *Store) SearchKB(ctx context.Context, q KBQuery) ([]KBResult, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	// Per-source limit: fetch up to q.Limit from each to allow for merge ranking.
	perLimit := q.Limit * 2
	if perLimit < 20 {
		perLimit = 20
	}

	var results []KBResult

	// ── Skills ─────────────────────────────────────────────────────────────
	if q.kindEnabled("skill") {
		skills, err := s.SearchSkills(ctx, q.Q, perLimit)
		if err != nil {
			return nil, fmt.Errorf("kb search skills: %w", err)
		}
		for _, sk := range skills {
			trgm := trgmSkill(sk, q.Q)
			payload, _ := json.Marshal(sk)
			results = append(results, KBResult{
				Kind:    "skill",
				ID:      sk.ID,
				Score:   combinedScore(trgm, sk.UpdatedAt),
				Summary: skillSummary(sk),
				Payload: payload,
			})
		}
	}

	// ── Memory ─────────────────────────────────────────────────────────────
	if q.kindEnabled("memory") {
		// ListMemory takes an optional kind filter — pass empty to get all kinds.
		items, err := s.ListMemory(ctx, "", perLimit)
		if err != nil {
			return nil, fmt.Errorf("kb search memory: %w", err)
		}
		for _, m := range items {
			trgm := trgmMemory(m, q.Q)
			payload, _ := json.Marshal(m)
			results = append(results, KBResult{
				Kind:    "memory",
				ID:      m.ID,
				Score:   combinedScore(trgm, m.UpdatedAt),
				Summary: memorySummary(m),
				Payload: payload,
			})
		}
	}

	// ── TechRadar ──────────────────────────────────────────────────────────
	if q.kindEnabled("radar") {
		entries, err := s.ListTechRadar(ctx)
		if err != nil {
			return nil, fmt.Errorf("kb search radar: %w", err)
		}
		for _, tr := range entries {
			trgm := trgmRadar(tr, q.Q)
			payload, _ := json.Marshal(tr)
			results = append(results, KBResult{
				Kind:    "radar",
				ID:      tr.ID,
				Score:   combinedScore(trgm, tr.UpdatedAt),
				Summary: radarSummary(tr),
				Payload: payload,
			})
		}
	}

	// ── Decisions ──────────────────────────────────────────────────────────
	if q.kindEnabled("decision") {
		decisions, err := s.ListDecisions(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("kb search decisions: %w", err)
		}
		for _, d := range decisions {
			trgm := trgmDecision(d, q.Q)
			payload, _ := json.Marshal(d)
			results = append(results, KBResult{
				Kind:    "decision",
				ID:      d.ID,
				Score:   combinedScore(trgm, d.UpdatedAt),
				Summary: decisionSummary(d),
				Payload: payload,
			})
		}
	}

	// Sort by score descending, then by kind+ID for determinism at equal scores.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return results[i].ID < results[j].ID
	})

	// Apply overall limit.
	if len(results) > q.Limit {
		results = results[:q.Limit]
	}
	return results, nil
}

// ── Similarity helpers ────────────────────────────────────────────────────────
// These approximate pg_trgm similarity in Go for the merged ranking step.
// We use a simple substring / token presence heuristic rather than the full
// trigram algorithm — the DB already pre-filtered by trgm, so this is only
// for ordering across heterogeneous result sets.

func trgmApprox(text, query string) float64 {
	if query == "" {
		return 0
	}
	text = strings.ToLower(text)
	query = strings.ToLower(query)
	if text == query {
		return 1.0
	}
	if strings.Contains(text, query) {
		return 0.7
	}
	// Word-level overlap.
	qWords := strings.Fields(query)
	matches := 0
	for _, w := range qWords {
		if strings.Contains(text, w) {
			matches++
		}
	}
	if len(qWords) == 0 {
		return 0
	}
	return float64(matches) / float64(len(qWords)) * 0.5
}

func trgmSkill(sk Skill, q string) float64 {
	return trgmApprox(sk.Name+" "+sk.Description+" "+sk.Category, q)
}

func trgmMemory(m MemoryItem, q string) float64 {
	return trgmApprox(m.Key+" "+m.Kind+" "+string(m.Value), q)
}

func trgmRadar(tr TechRadar, q string) float64 {
	return trgmApprox(tr.Tech+" "+tr.Quadrant+" "+tr.Ring+" "+tr.Note+" "+tr.Description, q)
}

func trgmDecision(d Decision, q string) float64 {
	return trgmApprox(d.ID+" "+d.Title+" "+d.Context+" "+d.Decision+" "+d.Why, q)
}

// ── Summary builders ─────────────────────────────────────────────────────────

func skillSummary(sk Skill) string {
	if sk.Description != "" {
		return truncate(sk.Name+": "+sk.Description, 200)
	}
	return sk.Name
}

func memorySummary(m MemoryItem) string {
	return truncate(fmt.Sprintf("[%s] %s: %s", m.Kind, m.Key, string(m.Value)), 200)
}

func radarSummary(tr TechRadar) string {
	s := fmt.Sprintf("%s (%s / %s)", tr.Tech, tr.Quadrant, tr.Ring)
	if tr.Note != "" {
		s += ": " + tr.Note
	}
	return truncate(s, 200)
}

func decisionSummary(d Decision) string {
	s := fmt.Sprintf("[%s] %s", d.Status, d.Title)
	if d.Decision != "" {
		s += ": " + d.Decision
	}
	return truncate(s, 200)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
