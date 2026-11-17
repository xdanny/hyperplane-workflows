package compose

import (
	"strings"
	"testing"
)

// ── Q-25: a brief nobody finished must not compose (and must not bill) ───────

func TestPlaceholderRefusal(t *testing.T) {
	cases := []struct {
		name  string
		brief string
		count int
	}{
		{"clean brief", "Audit the trip data for quality problems before the Q2 close.", 0},
		{"one blank", "Audit [replace with the dataset] for quality problems.", 1},
		{"shouted", "Audit [REPLACE WITH THE DATASET] and [Replace with the grain].", 2},
		{"spaced", "Compare [ replace with baseline ] against [replace with current].", 2},
		{"a real bracket is not a placeholder", "Follow up on ticket [1] and the note [sic].", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CountPlaceholders(c.brief); got != c.count {
				t.Fatalf("CountPlaceholders = %d, want %d", got, c.count)
			}
			msg := PlaceholderRefusal(c.brief)
			if c.count == 0 {
				if msg != "" {
					t.Fatalf("a finished brief must compose; got refusal %q", msg)
				}
				return
			}
			if !strings.Contains(msg, "'[replace with …]' placeholders") ||
				!strings.Contains(msg, "before requesting steps") {
				t.Errorf("refusal message lost its wording: %q", msg)
			}
			if !strings.Contains(msg, string(rune('0'+c.count))) {
				t.Errorf("refusal %q does not name the count %d", msg, c.count)
			}
		})
	}
}
