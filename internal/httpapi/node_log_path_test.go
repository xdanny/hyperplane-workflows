package httpapi

import (
	"path/filepath"
	"testing"
)

func TestNodeFilePathRejectsTraversal(t *testing.T) {
	runsDir := t.TempDir()

	bad := []struct {
		name   string
		runID  string
		nodeID string
	}{
		{"empty node id", "run-1", ""},
		{"dotdot", "run-1", "../secret"},
		{"dotdot chain", "run-1", "../../etc/passwd"},
		{"slash", "run-1", "sub/other"},
		{"backslash", "run-1", `\etc\passwd`},
		{"colon", "run-1", "c:passwd"},
		{"traversal in run id", "../run-1", "build"},
		{"slash in run id", "a/b", "build"},
		{"heal label with traversal", "run-1", "../evil.heal"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := nodeFilePath(runsDir, tc.runID, tc.nodeID, ".log"); err == nil {
				t.Fatalf("accepted %q/%q → %q, want a rejection", tc.runID, tc.nodeID, got)
			}
		})
	}

	// Dotted node ids are legitimate and must resolve inside the run dir.
	for _, nodeID := range []string{"build", "report-verify.heal", "a.b.c"} {
		got, err := nodeFilePath(runsDir, "run-1", nodeID, ".log")
		if err != nil {
			t.Fatalf("nodeFilePath(%q): %v", nodeID, err)
		}
		want := filepath.Join(runsDir, "run-1", nodeID+".log")
		if got != want {
			t.Errorf("nodeFilePath(%q) = %q, want %q", nodeID, got, want)
		}
	}
}
