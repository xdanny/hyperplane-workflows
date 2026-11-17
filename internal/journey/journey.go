// Package journey derives delivery summaries from saved runs, SDK checkpoints and human decisions.
package journey

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

type Stage string

const (
	StageBuilding      Stage = "building"
	StageVerifying     Stage = "verifying"
	StageNeedsDecision Stage = "needs_decision"
	// StageDone is a finished delivery whose workflow does not ask a person to
	// sign off. It is still decidable — the run page, the history and the
	// Inbox's decided view can all record one — it simply never queues.
	StageDone             Stage = "done"
	StageStopped          Stage = "stopped"
	StageRehearsed        Stage = "rehearsed"
	StageAccepted         Stage = "accepted"
	StageChangesRequested Stage = "changes_requested"
	StageRejected         Stage = "rejected"
)

type Maturity string

const (
	MaturityUnproven   Maturity = "unproven"
	MaturityProvenOnce Maturity = "proven_once"
	MaturityAccepted   Maturity = "accepted"
)

var terminalStates = map[string]bool{"passed": true, "failed": true, "interrupted": true}

func Terminal(state string) bool { return terminalStates[state] }

type Outcome struct {
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
	// DecidedBy names who recorded this verdict: "local" for the person at this
	// keyboard, "hyperplane:auto" for an acceptance the server recorded under a
	// workflow's sign-off policy. The distinction is not decoration — an
	// automatic acceptance is never a person's judgment.
	DecidedBy  string    `json:"decidedBy,omitempty"`
	DecidedAt  time.Time `json:"decidedAt"`
	DecisionID string    `json:"decisionId"`
	Scope      string    `json:"scope"`
	RunID      string    `json:"runId,omitempty"`
}
type Attempt struct {
	ContinuedFrom        string `json:"continuedFrom,omitempty"`
	Phase                string `json:"phase,omitempty"`
	RunID                string `json:"runId"`
	Number               int    `json:"number"`
	DAG                  string `json:"dag"`
	Goal                 string `json:"goal,omitempty"`
	State                string `json:"state"`
	Recipe               string `json:"recipe,omitempty"`
	Project              string `json:"project,omitempty"`
	Superseded           bool   `json:"superseded"`
	Rehearsal            bool   `json:"rehearsal,omitempty"`
	Criteria             int    `json:"criteria"`
	Proven               int    `json:"proven"`
	StandinProven        int    `json:"standinProven,omitempty"`
	DecisionCovered      bool   `json:"decisionCovered,omitempty"`
	FailedBeyondCriteria bool   `json:"failedBeyondCriteria,omitempty"`
	// SignOff is the sign-off policy this ATTEMPT was launched under.
	SignOff       string     `json:"signOff,omitempty"`
	FailingNode   string     `json:"failingNode,omitempty"`
	FailureReason string     `json:"failureReason,omitempty"`
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	Outcome       *Outcome   `json:"outcome,omitempty"`
}
type Event struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	RunID string    `json:"runId,omitempty"`
	Text  string    `json:"text"`
}
type Journey struct {
	Key           string    `json:"key"`
	Project       string    `json:"project,omitempty"`
	Name          string    `json:"name"`
	DAG           string    `json:"dag"`
	Recipe        string    `json:"recipe,omitempty"`
	RecipeName    string    `json:"recipeName,omitempty"`
	Root          string    `json:"root"`
	Stage         Stage     `json:"stage"`
	Maturity      Maturity  `json:"maturity"`
	Attempts      []Attempt `json:"attempts"`
	AttemptCount  int       `json:"attemptCount"`
	Criteria      int       `json:"criteria"`
	Proven        int       `json:"proven"`
	StandinProven int       `json:"standinProven,omitempty"`
	ProvenAttempt int       `json:"provenAttempt,omitempty"`
	Rehearsal     bool      `json:"rehearsal,omitempty"`
	// SignOff is the newest attempt's sign-off policy — the terms under which
	// this delivery is (or is not) waiting for a person.
	SignOff         string     `json:"signOff,omitempty"`
	Outcome         *Outcome   `json:"outcome,omitempty"`
	RecordedOutcome *Outcome   `json:"recordedOutcome,omitempty"`
	Coverage        *Coverage  `json:"coverage,omitempty"`
	NeedsDecision   bool       `json:"needsDecision"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	Timeline        []Event    `json:"timeline"`
}
type Bucket struct {
	Key        string
	Project    string
	DAG        string
	Recipe     string
	RecipeName string
	Root       string
	Runs       []store.Run
}
type Evidence struct {
	RunOutcomes    map[string]store.Decision
	JourneyOutcome *store.Decision
}

func Group(runs []store.Run) []Bucket {
	byID := make(map[string]store.Run, len(runs))
	for _, r := range runs {
		byID[r.RunID] = r
	}

	buckets := map[string]*Bucket{}
	roots := map[string]string{} // run id -> chain root
	var order []string

	for _, r := range runs {
		if r.ParentRun != "" && store.SDKContinuationSource(r) == "" {
			continue
		}
		root := chainRoot(r, byID)
		roots[r.RunID] = root
		ident := r
		if rootRun, ok := byID[root]; ok {
			ident = rootRun
		}
		recipe := store.RunDocString(ident, recipeKey)
		key := Key(ident.Project, ident.DAG, recipe, root)

		b, ok := buckets[key]
		if !ok {
			b = &Bucket{Key: key, Project: ident.Project, DAG: ident.DAG, Recipe: recipe}
			buckets[key] = b
			order = append(order, key)
		}
		b.Runs = append(b.Runs, r)
	}

	out := make([]Bucket, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		sortAttempts(b.Runs)
		if n := len(b.Runs); n > 0 {
			b.Root = roots[b.Runs[n-1].RunID]
		}
		out = append(out, *b)
	}
	return out
}

const recipeKey = "recipe"

func sortAttempts(runs []store.Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		ti, tj := attemptTime(runs[i]), attemptTime(runs[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return runs[i].RunID > runs[j].RunID
	})
	hoistContinuations(runs)
}
func attemptTime(r store.Run) time.Time {
	if !r.StartedAt.IsZero() {
		return r.StartedAt
	}
	return r.UpdatedAt
}
func hoistContinuations(runs []store.Run) {
	if len(runs) < 2 {
		return
	}
	for pass := 0; pass < len(runs); pass++ {
		moved := false
		for j := 1; j < len(runs); j++ {
			of := store.SDKContinuationSource(runs[j])
			if of == "" {
				continue
			}
			i := indexOfRun(runs[:j], of)
			if i < 0 {
				continue
			}
			repair := runs[j]
			copy(runs[i+1:j+1], runs[i:j])
			runs[i] = repair
			moved = true
		}
		if !moved {
			return
		}
	}
}
func indexOfRun(runs []store.Run, runID string) int {
	for i := range runs {
		if runs[i].RunID == runID {
			return i
		}
	}
	return -1
}
func RunIDs(buckets []Bucket) []string {
	var out []string
	for _, b := range buckets {
		for _, r := range b.Runs {
			out = append(out, r.RunID)
		}
	}
	return out
}
func everyAttemptRehearsed(attempts []Attempt) bool {
	if len(attempts) == 0 {
		return false
	}
	for _, a := range attempts {
		if !a.Rehearsal {
			return false
		}
	}
	return true
}
func Build(b Bucket, ev Evidence) Journey {
	j := Journey{
		Key:          b.Key,
		Project:      b.Project,
		DAG:          b.DAG,
		Recipe:       b.Recipe,
		RecipeName:   b.RecipeName,
		Root:         b.Root,
		AttemptCount: len(b.Runs),
		Attempts:     make([]Attempt, 0, len(b.Runs)),
		Timeline:     []Event{},
	}

	total := len(b.Runs)
	for i, r := range b.Runs {
		a := Attempt{
			RunID:   r.RunID,
			Number:  total - i, // oldest attempt is #1
			DAG:     r.DAG,
			Goal:    strings.TrimSpace(r.Goal),
			State:   r.State,
			Project: r.Project,
			Recipe:  store.RunDocString(r, recipeKey),

			UpdatedAt: r.UpdatedAt,
		}
		a.SignOff = store.RunSignOff(r)
		sdk := InspectRun(r)
		a.ContinuedFrom = sdk.ContinuedFrom
		a.Phase = sdk.Phase
		a.Superseded = i > 0
		a.Rehearsal = store.IsRehearsal(r)
		a.Criteria = len(sdk.Criteria)
		for _, criterion := range sdk.Criteria {
			if criterion.Proven {
				a.Proven++
			}
		}
		if a.Rehearsal {
			a.StandinProven, a.Proven = a.Proven, 0
		}
		if r.State != "passed" {
			a.FailingNode = sdk.CurrentNode
		}
		a.FailedBeyondCriteria = failedBeyondCriteria(a)
		if r.State == "failed" || r.State == "interrupted" {
			a.FailureReason = sdk.FailureReason
		}
		if !r.StartedAt.IsZero() {
			started := r.StartedAt
			a.StartedAt = &started
		}
		if !r.EndedAt.IsZero() {
			ended := r.EndedAt
			a.EndedAt = &ended
		}
		if d, ok := ev.RunOutcomes[store.OutcomeDecisionID(r.RunID)]; ok {
			a.Outcome = outcomeFrom(d, "run", r.RunID)
		}
		j.Attempts = append(j.Attempts, a)
	}

	if total > 0 {
		j.Coverage = pin(j.Attempts, ev.JourneyOutcome)
		newest := j.Attempts[0]
		j.Name = deliveryName(newest, b)
		j.Rehearsal = everyAttemptRehearsed(j.Attempts)
		tally := LatestTally(j.Attempts)
		j.Criteria, j.Proven, j.ProvenAttempt = tally.Criteria, tally.Proven, tally.Attempt
		j.StandinProven = tally.Standin
		j.UpdatedAt = newest.UpdatedAt
		oldest := j.Attempts[total-1]
		j.StartedAt = oldest.StartedAt
		if ev.JourneyOutcome != nil {
			decidedRun := ""
			for _, attempt := range j.Attempts {
				if ev.JourneyOutcome.ID == store.JourneyAttemptOutcomeDecisionID(b.Key, attempt.RunID) {
					decidedRun = attempt.RunID
					break
				}
			}
			j.RecordedOutcome = outcomeFrom(*ev.JourneyOutcome, "journey", decidedRun)
		}
		if j.Outcome = effectiveOutcome(j.RecordedOutcome, j.Coverage, newest); j.Outcome != nil {
			j.UpdatedAt = laterOf(j.UpdatedAt, j.Outcome.DecidedAt)
		}
		if j.RecordedOutcome != nil {
			j.UpdatedAt = laterOf(j.UpdatedAt, j.RecordedOutcome.DecidedAt)
		}
		j.SignOff = newest.SignOff
		j.Stage = deriveStage(j.Attempts, j.Outcome, j.SignOff)
	}

	j.NeedsDecision = j.Stage == StageNeedsDecision
	j.Maturity = deriveMaturity(j.Attempts, j.Outcome)
	j.Timeline = deriveTimeline(j)
	return j
}
func failedBeyondCriteria(a Attempt) bool {
	return a.State == "failed" && a.Criteria > 0 && a.Proven == a.Criteria
}
func deliveryName(newest Attempt, b Bucket) string {
	if newest.Goal != "" {
		return newest.Goal
	}
	if b.RecipeName != "" {
		return b.RecipeName
	}
	if b.Recipe != "" {
		return b.Recipe
	}
	return b.DAG
}
func effectiveOutcome(recorded *Outcome, cov *Coverage, newest Attempt) *Outcome {
	if recorded != nil && cov != nil && cov.CoversNewest {
		return recorded
	}
	return newest.Outcome
}
func outcomeFrom(d store.Decision, scope, runID string) *Outcome {
	at := d.CreatedAt
	if d.DecidedAt != nil {
		at = *d.DecidedAt
	}
	return &Outcome{
		Verdict:    d.Decision,
		Note:       d.Why,
		DecidedBy:  decidedBy(d),
		DecidedAt:  at,
		DecisionID: d.ID,
		Scope:      scope,
		RunID:      runID,
	}
}

// decidedBy names the decider of a stored decision, defaulting records written
// before the column existed to the person at this keyboard.
func decidedBy(d store.Decision) string {
	if d.DecidedBy == "" {
		return store.DecidedByLocal
	}
	return d.DecidedBy
}
func deriveStage(attempts []Attempt, outcome *Outcome, signOff string) Stage {
	for _, a := range attempts {
		if Terminal(a.State) {
			continue
		}
		if a.Phase == "verifying" {
			return StageVerifying
		}
		return StageBuilding
	}
	if outcome != nil {
		switch outcome.Verdict {
		case "accepted":
			return StageAccepted
		case "changes_requested":
			return StageChangesRequested
		case "rejected":
			return StageRejected
		}
	}
	if len(attempts) > 0 && attempts[0].State == "interrupted" {
		return StageStopped
	}
	if everyAttemptRehearsed(attempts) {
		return StageRehearsed
	}
	// The workflow says a person need not sign this off. The delivery is
	// finished and still decidable; it just stops asking.
	if signOff == store.SignOffOptional {
		return StageDone
	}
	return StageNeedsDecision
}
func deriveMaturity(attempts []Attempt, outcome *Outcome) Maturity {
	if outcome != nil && outcome.Verdict == verdictAccepted {
		return MaturityAccepted
	}
	for _, a := range attempts {
		if a.Rehearsal {
			continue
		}
		if a.Proven > 0 {
			return MaturityProvenOnce
		}
	}
	return MaturityUnproven
}
func deriveTimeline(j Journey) []Event {
	out := []Event{}
	for i := len(j.Attempts) - 1; i >= 0; i-- {
		a := j.Attempts[i]
		label := "Attempt " + strconv.Itoa(a.Number)
		if a.ContinuedFrom != "" {
			label += " (continued)"
		}
		if a.StartedAt != nil {
			out = append(out, Event{At: *a.StartedAt, Kind: "attempt_started", RunID: a.RunID, Text: label + " started."})
		}
		end := a.UpdatedAt
		if a.EndedAt != nil {
			end = *a.EndedAt
		}
		switch a.State {
		case "passed":
			text := label + " passed."
			if a.Criteria > 0 {
				text = label + " passed — " + strconv.Itoa(a.Proven) + "/" + strconv.Itoa(a.Criteria) + " checks passed."
			}
			out = append(out, Event{At: end, Kind: "attempt_passed", RunID: a.RunID, Text: text})
		case "failed":
			out = append(out, Event{At: end, Kind: "attempt_failed", RunID: a.RunID, Text: attemptFailedText(label, a)})
		case "interrupted":
			out = append(out, Event{At: end, Kind: "attempt_stopped", RunID: a.RunID, Text: label + " was stopped."})
		}
		if a.Outcome != nil {
			out = append(out, Event{At: a.Outcome.DecidedAt, Kind: "attempt_decided", RunID: a.RunID,
				Text: label + " recorded " + verdictPhrase(a.Outcome.Verdict) + "."})
		}
	}
	if j.RecordedOutcome != nil {
		text := "Delivery " + verdictPhrase(j.RecordedOutcome.Verdict) + "."
		if cov := j.Coverage; cov != nil && !cov.CoversNewest {
			text = "Delivery " + verdictPhrase(j.RecordedOutcome.Verdict) + ", covering attempt" +
				plural(len(cov.Attempts)) + " " + AttemptRange(cov.Attempts) + "."
			if len(cov.Attempts) == 0 {
				text = "Delivery " + verdictPhrase(j.RecordedOutcome.Verdict) +
					" — it covers no attempt this delivery still has."
			}
		}
		out = append(out, Event{At: j.RecordedOutcome.DecidedAt, Kind: "decided", Text: text})
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].At.Before(out[k].At) })
	return out
}
func attemptFailedText(label string, a Attempt) string {
	where := label + " failed"
	if a.FailingNode != "" {
		where += " at " + a.FailingNode
	}

	if a.FailureReason != "" {
		text := where + ": " + ensureStop(a.FailureReason)
		if a.FailedBeyondCriteria {
			if a.Criteria == 1 {
				text += " Its configured check passed."
			} else {
				text += " All " + strconv.Itoa(a.Criteria) + " configured checks passed."
			}
		}
		return text
	}
	if a.FailedBeyondCriteria {
		return where + " — " + strconv.Itoa(a.Proven) + "/" + strconv.Itoa(a.Criteria) +
			" checks passed; what failed was not one of them."
	}
	return where + "."
}
func ensureStop(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "…") {
		return s
	}
	switch s[len(s)-1] {
	case '.', '!', '?', ':', ';':
		return s
	}
	return s + "."
}
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
func verdictPhrase(verdict string) string {
	switch verdict {
	case "changes_requested":
		return "changes requested"
	default:
		return verdict
	}
}
func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
func Find(journeys []Journey, key string) (Journey, bool) {
	for _, j := range journeys {
		if j.Key == key {
			return j, true
		}
	}
	return Journey{}, false
}
func NeedingDecision(journeys []Journey) []Journey {
	out := []Journey{}
	for _, j := range journeys {
		if j.NeedsDecision {
			out = append(out, j)
		}
	}
	return out
}
