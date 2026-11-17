package brief

// ground.go — the grounding check.
//
// A brief is prose a person will act on, written by a model that was handed a
// state document. The one failure mode that matters is a number the state does
// not support: "312 of 4,000 rows" reads as measurement whatever its
// provenance, and a reader has no way to tell a copied figure from a
// remembered one. So every numeric token in a generated brief is extracted and
// looked for in the state document it was written from.
//
// WHAT THIS CATCHES: a number that is not in the state at all — invented,
// remembered from training, or computed by the model out of two other numbers.
//
// WHAT IT DOES NOT CATCH, and no reader should believe it does: a number that
// IS in the state, quoted accurately, and attached to the wrong claim. If the
// scorecard says PSI 0.3058 with the rule marked shifted, a brief that writes
// "0.3058, comfortably within tolerance" passes this check — the number is
// grounded, the sentence is false. Grounding is a provenance check, not a
// truth check. Catching mis-attribution needs a reader, or a judge; it is not
// what this function claims.

import (
	"regexp"
	"strconv"
	"strings"
)

// numberPattern matches a numeric token: an optional sign is deliberately NOT
// consumed (a minus in prose is usually a dash), digits with optional thousand
// separators, an optional fraction, and an optional trailing percent.
var numberPattern = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?%?`)

// smallIntCeiling is the largest bare integer treated as ordinary counting
// language rather than a measurement. "the three checks", "one of two" and
// "step 4" are not claims about data; requiring them to appear in the state
// document would flag every readable sentence.
const smallIntCeiling = 12

// yearFloor / yearCeiling bracket the integers read as a calendar year. A date
// in a sentence is not a measurement either.
const (
	yearFloor   = 1900
	yearCeiling = 2100
)

// minSigDigitsForRounding is how many significant digits a WHOLE NUMBER needs
// before a rounded match is allowed. Below it, only an exact match counts: "8"
// rounding to any value in [7.5, 8.5) would ground almost anything. A token
// with a decimal point is exempt — writing "0.31" is a claim about precision,
// and refusing it would force a brief to quote 0.3058 or say nothing.
const minSigDigitsForRounding = 3

// number is one extracted numeric token, kept alongside the text it was written
// as so an unverified number can be reported back in the author's own spelling.
type number struct {
	// Token is the token exactly as it appeared ("4,000", "7.8%", "0.3058").
	Token string
	// Value is the parsed magnitude, with commas and any percent sign removed.
	Value float64
	// Decimals is how many digits followed the point, which is the precision
	// the author claimed.
	Decimals int
	// SigDigits is how many significant digits the token carries.
	SigDigits int
	// Canon is the normalised comparison form: commas and percent stripped,
	// leading zeros left alone.
	Canon string
}

// extractNumbers returns every numeric token in a text, in order of appearance.
func extractNumbers(text string) []number {
	matches := numberPattern.FindAllString(text, -1)
	out := make([]number, 0, len(matches))
	for _, m := range matches {
		if n, ok := parseNumber(m); ok {
			out = append(out, n)
		}
	}
	return out
}

// parseNumber normalises one token. It fails on anything that will not parse,
// which is how a malformed match ("1,2,3") is dropped rather than guessed at.
func parseNumber(token string) (number, bool) {
	canon := strings.TrimSuffix(token, "%")
	canon = strings.ReplaceAll(canon, ",", "")
	if canon == "" {
		return number{}, false
	}
	v, err := strconv.ParseFloat(canon, 64)
	if err != nil {
		return number{}, false
	}
	decimals := 0
	if i := strings.IndexByte(canon, '.'); i >= 0 {
		decimals = len(canon) - i - 1
	}
	return number{
		Token: token, Value: v, Decimals: decimals,
		SigDigits: significantDigits(canon), Canon: canon,
	}, true
}

// significantDigits counts the digits that carry information: leading zeros
// (including the zero before a decimal point) do not.
func significantDigits(canon string) int {
	digits := strings.ReplaceAll(canon, ".", "")
	digits = strings.TrimLeft(digits, "0")
	// A trailing-zero digit after a decimal point IS significant ("0.30" claims
	// two), so only the leading zeros are stripped.
	return len(digits)
}

// whitelisted reports whether a token is ordinary counting or date language
// rather than a measurement.
func whitelisted(n number) bool {
	if n.Decimals > 0 {
		return false
	}
	if n.Value >= 0 && n.Value <= smallIntCeiling {
		return true
	}
	return n.Value >= yearFloor && n.Value <= yearCeiling && n.Value == float64(int64(n.Value))
}

// grounds reports whether a state-document number supports an authored one.
//
// Two ways: the same token (after normalisation), or the state's number rounded
// to the author's own precision. The second is what lets a brief write "0.31"
// for a measured 0.3058 — a legitimate, readable rounding — without letting it
// write "8" for anything between 7.5 and 8.5.
func grounds(source, authored number) bool {
	if source.Canon == authored.Canon {
		return true
	}
	if authored.Decimals == 0 && authored.SigDigits < minSigDigitsForRounding {
		return false
	}
	return strconv.FormatFloat(source.Value, 'f', authored.Decimals, 64) == authored.Canon
}

// Ungrounded returns the numeric tokens in text that the state document does
// not support, in order of appearance, without duplicates.
//
// An empty result means every number in the text came from the state. It does
// NOT mean every number is used correctly — see the file header.
func Ungrounded(text, stateDoc string) []string {
	authored := extractNumbers(text)
	if len(authored) == 0 {
		return nil
	}
	sources := extractNumbers(stateDoc)

	var out []string
	seen := map[string]bool{}
	for _, a := range authored {
		if whitelisted(a) || seen[a.Token] {
			continue
		}
		found := false
		for _, s := range sources {
			if grounds(s, a) {
				found = true
				break
			}
		}
		if !found {
			seen[a.Token] = true
			out = append(out, a.Token)
		}
	}
	return out
}
