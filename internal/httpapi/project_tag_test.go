package httpapi

import "testing"

// TestProjectTag pins the normalization every project-scoped write shares.
// Empty stays legal (unscoped is a real answer), whitespace collapses to it,
// and anything that could not survive as a /api/projects/{id} path segment is
// rejected at the door rather than 404-ing later.
func TestProjectTag(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty is unscoped", "", "", false},
		{"whitespace is unscoped", "   ", "", false},
		{"plain id", "acme-launch", "acme-launch", false},
		{"trimmed", "  acme-launch  ", "acme-launch", false},
		{"spaces are allowed inside", "Acme Launch", "Acme Launch", false},
		{"traversal rejected", "../etc", "", true},
		{"slash rejected", "acme/launch", "", true},
		{"wildcards rejected", `acme*?"`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectTag(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("projectTag(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("projectTag(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("projectTag(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
