package brief

import (
	"strings"
	"testing"
)

// TestUngroundedNumbers is the table for the one check that stands between a
// reader and a fabricated measurement. Each case names what it is defending.
func TestUngroundedNumbers(t *testing.T) {
	state := `run: r-1
final state: failed
4 of 5 criteria proven
rows scanned: 4,000
violations: 312 (7.8%)
psi: 0.3058
tolerance: 0.25
rule volume-drift value=1234567
decided: 2026-08-21T09:15:00Z`

	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "every number copied from the state is grounded",
			text: "312 of 4,000 rows (7.8%) violated the rule; PSI was 0.3058 against a tolerance of 0.25.",
		},
		{
			name: "a number that is nowhere in the state is caught",
			text: "The audit checked 9,412 rows.",
			want: []string{"9,412"},
		},
		{
			name: "a plausible near-miss is still ungrounded",
			text: "PSI came out at 0.3057.",
			want: []string{"0.3057"},
		},
		{
			name: "commas and percent signs normalise, so the same figure written differently still matches",
			text: "4000 rows, 7.8 percent of them flagged, one rule at 1,234,567.",
		},
		{
			name: "small integers are counting language, not measurements",
			text: "Two of the three gates ran; step 4 stopped the run and 11 steps never started.",
		},
		{
			name: "a year is a date, not a measurement",
			text: "The window covers 1999 through 2026.",
		},
		{
			name: "a rounded figure is grounded by the precise one it rounds from",
			text: "PSI was about 0.31, well past the line.",
		},
		{
			name: "rounding is not a licence for a coarse integer to match anything",
			text: "The rule flagged 47 rows.",
			want: []string{"47"},
		},
		{
			name: "a computed number the state never stated is caught",
			text: "That leaves 3,688 clean rows.",
			want: []string{"3,688"},
		},
		{
			name: "each offender is reported once, in the author's own spelling",
			text: "9,412 rows, and again 9,412 rows.",
			want: []string{"9,412"},
		},
		{
			name: "prose with no numbers at all is grounded",
			text: "The review gate rejected the report. Nothing was delivered.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Ungrounded(tc.text, state)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Ungrounded() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUngroundedIsProvenanceNotTruth pins the honest limit of this check, so
// nobody reads a `grounded` badge as "the sentence is correct". The Q-1 case:
// the scorecard says the rule SHIFTED at 0.3058, and the brief quotes 0.3058
// while claiming the opposite. Grounding passes. It has to — the number really
// is in the state — and the comment in ground.go says so out loud.
func TestUngroundedIsProvenanceNotTruth(t *testing.T) {
	state := "rule psi-tenure value=0.3058 tolerance=0.25 shifted=true"
	misattributed := "Tenure moved by 0.3058, comfortably inside its tolerance."
	if got := Ungrounded(misattributed, state); len(got) != 0 {
		t.Fatalf("expected the mis-attributed number to pass the grounding check (it is a provenance check, not a truth check); got %v", got)
	}
}

func TestExtractNumbersKeepsTheAuthorsSpelling(t *testing.T) {
	got := extractNumbers("4,000 rows at 7.8% and 0.3058")
	want := []string{"4,000", "7.8%", "0.3058"}
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d: %+v", len(got), len(want), got)
	}
	for i, n := range got {
		if n.Token != want[i] {
			t.Errorf("token %d = %q, want %q", i, n.Token, want[i])
		}
	}
	if got[0].Value != 4000 || got[1].Value != 7.8 || got[2].Decimals != 4 {
		t.Errorf("normalisation lost something: %+v", got)
	}
}

func TestSignificantDigits(t *testing.T) {
	cases := map[string]int{"0.3058": 4, "312": 3, "4000": 4, "0.25": 2, "0.007": 1, "8": 1}
	for in, want := range cases {
		if got := significantDigits(in); got != want {
			t.Errorf("significantDigits(%q) = %d, want %d", in, got, want)
		}
	}
}
