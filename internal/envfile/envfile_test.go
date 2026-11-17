package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSetsUnsetVarsAndRespectsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "" +
		"# a comment\n" +
		"\n" +
		"CONTEXT7_API_KEY=ctx7sk-abc123\n" +
		`export QUOTED="with spaces"` + "\n" +
		"ALREADY_SET=fromfile\n" +
		"malformed line without equals\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ALREADY_SET", "fromenv") // real env must win
	os.Unsetenv("CONTEXT7_API_KEY")
	os.Unsetenv("QUOTED")

	Load(path)

	if got := os.Getenv("CONTEXT7_API_KEY"); got != "ctx7sk-abc123" {
		t.Errorf("CONTEXT7_API_KEY = %q, want ctx7sk-abc123", got)
	}
	if got := os.Getenv("QUOTED"); got != "with spaces" {
		t.Errorf("QUOTED = %q, want 'with spaces' (quotes stripped, export prefix handled)", got)
	}
	if got := os.Getenv("ALREADY_SET"); got != "fromenv" {
		t.Errorf("ALREADY_SET = %q, want fromenv (real env must take precedence)", got)
	}
}

func TestLoadMissingFileIsSilent(t *testing.T) {
	Load(filepath.Join(t.TempDir(), "does-not-exist.env")) // must not panic
}

func TestExplicitEmptyEnvironmentWins(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("DATABASE_URL=production\n"), 0600); err != nil {
		t.Fatal(err)
	}
	Load(path)
	if os.Getenv("DATABASE_URL") != "" {
		t.Fatal("env file restored a deliberately disabled connection")
	}
}
