package compose

import (
	"fmt"
	"regexp"
)

// ── Placeholder refusal (Q-25) ───────────────────────────────────────────────
//
// The five data-recipe starter chips used to paste a 150–250-word brief made of
// "[replace with …]" blanks into the free-text composer, and nothing stopped an
// unedited one from being composed — which spends 30–90 s of billed orchestrator
// time planning against instructions nobody wrote. A brief that still carries a
// placeholder is refused, in both directions: the client will not send it and
// the server will not plan it.

// placeholderPattern matches the bracketed blank the starter briefs ship with,
// case-insensitively, and the shouted "[REPLACE" variant. It deliberately does
// not match every bracket — "[1]" or "[sic]" in a real brief is a person's own
// prose and none of the platform's business.
var placeholderPattern = regexp.MustCompile(`(?i)\[\s*replace\b`)

// CountPlaceholders reports how many unfilled "[replace with …]" blanks a brief
// still carries.
func CountPlaceholders(brief string) int {
	return len(placeholderPattern.FindAllString(brief, -1))
}

// PlaceholderRefusal returns the message to show for a brief that still carries
// placeholders, or "" when the brief is fit to compose. The wording is fixed:
// it names the count so the person can find them, and points at the recipe form
// as the other door to the same workflow.
func PlaceholderRefusal(brief string) string {
	n := CountPlaceholders(brief)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Your brief still has %d '[replace with …]' placeholders — replace them with concrete details before requesting steps.", n)
}
