package persona

// routing.go — "who is qualified to judge this?"
//
// This is the hook the future auto-trigger hangs on. Today it answers a
// question a human asks (GET /api/personas?competentFor=skill:x); tomorrow the
// improve loop and the extraction trigger ask the same question at the moment a
// refinement is proposed or a draft appears (spec AI-12). Keeping it a pure
// function of the parsed competence is what makes that a wiring change rather
// than a redesign.

import (
	"fmt"
	"strings"
)

// Target is what a review is aimed at: a run tree, or a skill/draft.
type Target struct {
	Kind string // "run" | "skill"
	ID   string
}

// Target kinds. There are exactly two, and both are things the platform can
// hand a read-only agent as evidence. A browser UX audit is not one of them —
// see the package doc.
const (
	TargetRun   = "run"
	TargetSkill = "skill"
)

// String renders the wire form ("run:20260820-101015.476-pr-market-research").
func (t Target) String() string { return t.Kind + ":" + t.ID }

// RecordKey renders the target for use inside a record id — the same value as
// String with the separator swapped, so a review id stays a plain token that is
// safe in a URL path segment.
func (t Target) RecordKey() string { return t.Kind + "-" + t.ID }

// ParseTarget reads the "kind:id" wire form.
//
// It refuses anything it does not recognise rather than guessing: an unknown
// target kind is a 400, because the alternative is assembling a review prompt
// out of evidence nobody collected.
func ParseTarget(s string) (Target, error) {
	kind, id, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || id == "" {
		return Target{}, fmt.Errorf("target must be %q or %q, got %q", "run:<runId>", "skill:<skillId>", s)
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != TargetRun && kind != TargetSkill {
		return Target{}, fmt.Errorf("unknown target kind %q — expected 'run' or 'skill'", kind)
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return Target{}, fmt.Errorf("invalid target id %q", id)
	}
	return Target{Kind: kind, ID: id}, nil
}

// Match reports whether a persona claims competence over a target, and on what
// grounds. The reason is returned because a routing answer nobody can check is
// the same problem as a verdict nobody can check.
//
// It matches ONLY on what the charter named outright (see Competence): the
// exact skill or process id, or a universal claim over wording. Domains are
// display-only — an English sentence is not matched against a skill id, because
// a plausible-looking wrong reviewer is worse than no reviewer at all.
func Match(c Competence, t Target) (bool, string) {
	if t.Kind != TargetSkill {
		// Run reviews are not routed by competence today: which persona should
		// read a delivery is a judgement about the delivery, not about a skill
		// id, and inferring it would be exactly the over-claim this function
		// exists to avoid. A human picks; the endpoint runs whoever they picked.
		return false, ""
	}
	id := strings.ToLower(t.ID)
	if containsFold(c.Skills, id) {
		return true, "names this skill in their COMPETENCE section"
	}
	if containsFold(c.Processes, id) {
		return true, "names this recipe/process in their COMPETENCE section"
	}
	if c.Wording {
		return true, "reviews the wording of any skill instruction — additive to, never a substitute for, a domain review"
	}
	return false, ""
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}
