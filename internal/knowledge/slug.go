package knowledge

import (
	"regexp"
	"strings"
)

// slugRe matches any character that is NOT alphanumeric, underscore, or hyphen.
var slugRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// slugify converts an arbitrary string into a safe filename component.
// Any run of non-alphanumeric characters is replaced by a single hyphen.
// The result is trimmed and capped at 200 characters.
func slugify(s string) string {
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		s = "item"
	}
	return s
}
