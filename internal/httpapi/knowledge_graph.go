package httpapi

import (
	"net/http"
	"sort"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// kgNode is one node in the knowledge graph.
type kgNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // decision | skill
	Label     string `json:"label"`
	Status    string `json:"status,omitempty"`
	Community int    `json:"community"`
	Degree    int    `json:"degree"`
}

// kgEdge is a relationship between two knowledge nodes.
type kgEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // ref | tag | topic | uses | category
}

// knowledgeGraphResponse is the body of GET /api/graph/knowledge.
type knowledgeGraphResponse struct {
	Nodes       []kgNode       `json:"nodes"`
	Edges       []kgEdge       `json:"edges"`
	Communities int            `json:"communities"`
	Counts      map[string]int `json:"counts"`
}

// handleKnowledgeGraph builds a self-organizing graph across knowledge kinds:
// decisions and skills. Edges: decision refs (ADR→ADR), shared tags,
// content-similarity topic links, decision→skill mentions ("uses"), and
// skill↔skill shared-category links. Communities are connected components.
//
// GET /api/graph/knowledge?kinds=decision,skill  (default: all)
func (s *Server) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	want := func(k string) bool {
		q := r.URL.Query().Get("kinds")
		if q == "" {
			return true
		}
		for _, p := range strings.Split(q, ",") {
			if strings.TrimSpace(p) == k {
				return true
			}
		}
		return false
	}

	ctx := r.Context()
	var decisions []store.Decision
	var skills []store.Skill
	var err error

	project := projectFilter(r)
	if want("decision") {
		if decisions, err = s.store.ListDecisions(ctx, project); err != nil {
			writeJSON(w, http.StatusInternalServerError, errBody(err))
			return
		}
	}
	if want("skill") {
		if skills, err = s.store.ListSkills(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, errBody(err))
			return
		}
	}

	var nodes []kgNode
	exists := map[string]bool{}
	nodeID := func(kind, id string) string { return kind + ":" + id }
	add := func(kind, id, label, status string) {
		nid := nodeID(kind, id)
		exists[nid] = true
		nodes = append(nodes, kgNode{ID: nid, Kind: kind, Label: label, Status: status})
	}
	for _, d := range decisions {
		add("decision", d.ID, decisionLabel(d), d.Status)
	}
	for _, sk := range skills {
		add("skill", sk.ID, sk.Name, "")
	}

	uf := newUnionFind()
	for _, n := range nodes {
		uf.add(n.ID)
	}
	var edges []kgEdge
	seen := map[string]bool{}
	addEdge := func(a, b, kind string) {
		if a == b || !exists[a] || !exists[b] || seen[a+"→"+b+"|"+kind] {
			return
		}
		seen[a+"→"+b+"|"+kind] = true
		edges = append(edges, kgEdge{From: a, To: b, Kind: kind})
		uf.union(a, b)
	}
	connected := map[string]bool{}

	// Decision↔decision: explicit refs + shared tags.
	for _, d := range decisions {
		for _, ref := range d.Refs {
			addEdge(nodeID("decision", d.ID), nodeID("decision", ref), "ref")
			connected[pairKey(d.ID, ref)] = true
		}
	}
	byTag := map[string][]string{}
	for _, d := range decisions {
		for _, t := range d.Tags {
			byTag[t] = append(byTag[t], d.ID)
		}
	}
	for _, ids := range byTag {
		sort.Strings(ids)
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				addEdge(nodeID("decision", ids[i]), nodeID("decision", ids[j]), "tag")
				connected[pairKey(ids[i], ids[j])] = true
			}
		}
	}

	// Decision↔decision: content-similarity topic edges (greedy, capped).
	terms := map[string]map[string]bool{}
	for _, d := range decisions {
		terms[d.ID] = significantTerms(d)
	}
	type cand struct {
		a, b string
		ov   int
	}
	var cands []cand
	for i := 0; i < len(decisions); i++ {
		for j := i + 1; j < len(decisions); j++ {
			a, b := decisions[i].ID, decisions[j].ID
			if connected[pairKey(a, b)] {
				continue
			}
			if ov := termOverlap(terms[a], terms[b]); ov >= 3 {
				cands = append(cands, cand{a, b, ov})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].ov != cands[j].ov {
			return cands[i].ov > cands[j].ov
		}
		return cands[i].a+cands[i].b < cands[j].a+cands[j].b
	})
	topicDeg := map[string]int{}
	for _, c := range cands {
		if topicDeg[c.a] >= 3 || topicDeg[c.b] >= 3 {
			continue
		}
		before := len(edges)
		addEdge(nodeID("decision", c.a), nodeID("decision", c.b), "topic")
		if len(edges) > before {
			topicDeg[c.a]++
			topicDeg[c.b]++
		}
	}

	// Decision→skill: name mentioned in the decision text.
	for _, d := range decisions {
		text := strings.ToLower(strings.Join([]string{d.Title, d.Context, d.Decision, d.Why, d.Alternatives, d.Consequences}, " "))
		for _, sk := range skills {
			if mentions(text, sk.Name) || mentions(text, sk.ID) {
				addEdge(nodeID("decision", d.ID), nodeID("skill", sk.ID), "uses")
			}
		}

	}

	// Skill↔skill: shared category.
	byCat := map[string][]string{}
	for _, sk := range skills {
		if sk.Category != "" {
			byCat[sk.Category] = append(byCat[sk.Category], sk.ID)
		}
	}
	for _, ids := range byCat {
		sort.Strings(ids)
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				addEdge(nodeID("skill", ids[i]), nodeID("skill", ids[j]), "category")
			}
		}
	}

	// Degrees + communities.
	deg := map[string]int{}
	for _, e := range edges {
		deg[e.From]++
		deg[e.To]++
	}
	commID := map[string]int{}
	next := 0
	for i := range nodes {
		root := uf.find(nodes[i].ID)
		id, ok := commID[root]
		if !ok {
			id, next = next, next+1
			commID[root] = id
		}
		nodes[i].Community = id
		nodes[i].Degree = deg[nodes[i].ID]
	}

	writeJSON(w, http.StatusOK, knowledgeGraphResponse{
		Nodes:       nodes,
		Edges:       edges,
		Communities: next,
		Counts: map[string]int{
			"decision": len(decisions),
			"skill":    len(skills),
		},
	})
}

// mentions reports whether text contains name as a meaningful substring (name
// must be ≥5 chars to avoid spurious hits).
func mentions(text, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return len(name) >= 5 && strings.Contains(text, name)
}

func decisionLabel(d store.Decision) string {
	if d.Title != "" {
		return d.ID + " · " + d.Title
	}
	return d.ID
}

// pairKey is an order-independent key for a node pair.
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// kgStopwords are common words ignored when deriving topic edges.
var kgStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "are": true, "was": true, "but": true, "not": true, "via": true,
	"use": true, "used": true, "using": true, "into": true, "per": true, "its": true,
	"can": true, "will": true, "all": true, "any": true, "one": true, "two": true,
	"now": true, "new": true, "only": true, "each": true, "both": true, "when": true,
	"then": true, "than": true, "over": true, "user": true, "userq": true, "decision": true,
	"context": true, "status": true, "accepted": true, "consequences": true,
	"alternatives": true, "why": true, "adr": true, "date": true,
}

// significantTerms returns meaningful lowercase terms (alnum, length ≥ 4, not a
// stopword) across a decision's prose.
func significantTerms(d store.Decision) map[string]bool {
	text := strings.ToLower(strings.Join([]string{
		d.Title, d.Context, d.Decision, d.Why, d.Alternatives, d.Consequences,
		strings.Join(d.Tags, " "),
	}, " "))
	set := map[string]bool{}
	var b strings.Builder
	flush := func() {
		if b.Len() >= 4 {
			t := b.String()
			if !kgStopwords[t] {
				set[t] = true
			}
		}
		b.Reset()
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return set
}

// termOverlap counts terms shared by two sets.
func termOverlap(a, b map[string]bool) int {
	small, big := a, b
	if len(b) < len(a) {
		small, big = b, a
	}
	n := 0
	for t := range small {
		if big[t] {
			n++
		}
	}
	return n
}

// ── union-find ────────────────────────────────────────────────────────────────

type unionFind struct{ parent map[string]string }

func newUnionFind() *unionFind { return &unionFind{parent: map[string]string{}} }

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	u.add(x)
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}
