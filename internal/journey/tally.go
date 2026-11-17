package journey

// tally.go — the ONE place a delivery's proof count is decided.
//
// The disease this file cures: the project panel said "4/5 proven", the
// deliveries view said "5/5 proven", and the run page said "5/5 proven" about
// the SAME delivery. 4/5 was attempt 1's count, 5/5 attempt 2's. Every surface
// was computing "how much of this delivery is proven" for itself, and one of
// them reached for the wrong attempt.
//
// A delivery's proof count is a statement about the CURRENT state of the
// product, so it comes from the LATEST attempt and from nothing else. It is
// never a sum: a delivery does not get more proven by failing more often, and
// it does not stay less proven because an earlier attempt left a promise
// unchecked. Every surface that names this delivery reads LatestTally, and the
// wire carries the attempt number the count came from so a chip can say WHICH
// attempt it is quoting ("5/5 proven · attempt 2") instead of leaving a reader
// to reconcile two numbers.

// Tally is a delivery's proof count and the attempt it was counted from.
//
// Attempt/RunID are not decoration. A count with no attempt beside it is
// exactly the artefact that let two surfaces disagree in silence: both said
// "N/5 proven" and neither said which chapter it was reading.
type Tally struct {
	Criteria int `json:"criteria"`
	Proven   int `json:"proven"`
	// Standin is how many criteria the attempt cleared with stand-ins rather
	// than proof — non-zero only for a rehearsal, where Proven is 0. It rides
	// alongside so a chip can say "0/6 proven — 6 passed by stand-ins" instead
	// of dropping six checks a reader watched go green.
	Standin int `json:"standin,omitempty"`
	// Attempt is the chapter number the counts came from (1 is the oldest).
	Attempt int `json:"attempt,omitempty"`
	// RunID is that chapter's run.
	RunID string `json:"runId,omitempty"`
}

// Empty reports whether this delivery promised nothing to count.
func (t Tally) Empty() bool { return t.Criteria <= 0 }

// LatestTally is THE delivery-grain proof count: the newest attempt's, always.
//
// It picks by attempt NUMBER rather than by slice position on purpose. Callers
// pass attempts newest-first by convention, and a convention is precisely what
// a second surface forgets — picking the highest chapter number is true however
// the slice arrived.
func LatestTally(attempts []Attempt) Tally {
	var out Tally
	found := false
	for _, a := range attempts {
		if found && a.Number <= out.Attempt {
			continue
		}
		out = Tally{Criteria: a.Criteria, Proven: a.Proven, Standin: a.StandinProven, Attempt: a.Number, RunID: a.RunID}
		found = true
	}
	return out
}
