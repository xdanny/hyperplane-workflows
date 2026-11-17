package sdkworkflow

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidationPreservesOmittedNullAndExplicitEmptyCommands(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		valid       bool
	}{
		{"omitted", "", true},
		{"null check", `,"check":null`, true},
		{"empty check", `,"check":[]`, false},
		{"blank program", `,"check":[""]`, false},
		{"null inputs", `,"inputs":null`, true},
		{"empty inputs", `,"inputs":{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var node Node
			if err := json.Unmarshal([]byte(`{"id":"work","kind":"task","title":"Work","instruction":"Create a file"`+tc.extra+`}`), &node); err != nil {
				t.Fatal(err)
			}
			err := Validate(t.Context(), "Create a file", []Node{node})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if err != nil {
				var issue *ValidationIssue
				if !errors.As(err, &issue) || errors.Is(err, ErrValidationUnavailable) {
					t.Fatalf("semantic issue lost: %v", err)
				}
			}
		})
	}
}
