package journey

import (
	"testing"
)

func TestLatestAttemptWins(t *testing.T) {
	attempts := []Attempt{
		{RunID: "r2", Number: 2, Criteria: 5, Proven: 5},
		{RunID: "r1", Number: 1, Criteria: 5, Proven: 4},
	}
	got := LatestTally(attempts)
	if got.Criteria != 5 || got.Proven != 5 {
		t.Errorf("LatestTally = %d/%d proven, want 5/5 — attempt 2's count", got.Proven, got.Criteria)
	}
	if got.Attempt != 2 || got.RunID != "r2" {
		t.Errorf("LatestTally came from attempt %d (%s), want attempt 2 (r2)", got.Attempt, got.RunID)
	}
}
func TestLatestTallyIgnoresSliceOrder(t *testing.T) {
	oldestFirst := []Attempt{
		{RunID: "r1", Number: 1, Criteria: 5, Proven: 4},
		{RunID: "r2", Number: 2, Criteria: 5, Proven: 5},
	}
	if got := LatestTally(oldestFirst); got.Proven != 5 || got.Attempt != 2 {
		t.Errorf("LatestTally = %d proven from attempt %d, want 5 from attempt 2", got.Proven, got.Attempt)
	}
	if got := LatestTally(nil); !got.Empty() || got.Attempt != 0 {
		t.Errorf("LatestTally(nil) = %+v, want an empty tally", got)
	}
}
