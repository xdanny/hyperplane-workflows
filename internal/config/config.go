// Package config centralizes environment-derived settings for the Hyperplane
// API and CLI. Local-first defaults: loopback bind, a local Postgres, and a
// separate default target workspace.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultImproveInterval is the self-improvement rhythm when nothing overrides
// it. Kept here rather than imported from internal/improve so config stays a
// leaf package that every command can load cheaply; internal/improve documents
// the same figure as its own default.
const defaultImproveInterval time.Duration = 0

// Config holds runtime settings resolved from the environment.
type Config struct {
	// Addr is the host:port the API binds. Loopback only by default.
	Addr string
	// DatabaseURL is the Postgres DSN.
	DatabaseURL string
	// PlatformRoot is the Hyperplane install root (where packages/ and schemas/ live).
	PlatformRoot string
	// TargetRoot is the repository a workflow operates against.
	TargetRoot string
	// RunsDir is where per-run artifacts/logs are projected on disk.
	RunsDir string
	// Token, when set (HYPERPLANE_TOKEN), is a bearer secret required on every
	// non-loopback request. Loopback (127.0.0.1/::1) stays open so the local CLI
	// and dev server keep working with no token. Empty ⇒ loopback-only posture
	// (the original golden rule): non-loopback requests are refused outright.
	Token string
	// ShellCmd is the program launched for the web "Shell" terminal tab. Defaults
	// to the user's login shell.
	ShellCmd string
	// OrchestratorCmd is the program launched for the web "Orchestrator" terminal
	// tab. Defaults to the SDK console; explicit overrides are trusted host programs.
	OrchestratorCmd string
	// AllowedOrigins are extra browser origins permitted to open the terminal
	// WebSocket (CSRF defense). The dev web origin is always allowed; add the
	// remote/tunnel origin via HYPERPLANE_ALLOWED_ORIGINS (comma-separated).
	AllowedOrigins []string
	// WebDir, when set (HYPERPLANE_WEB_DIR), is a built web bundle the API serves
	// directly so the whole app is single-origin (no separate web server / proxy)
	// — the simplest secure shape behind a tunnel. Empty ⇒ API-only.
	WebDir string
	// ImproveInterval is how often the API runs the self-improvement pass in
	// process (HYPERPLANE_IMPROVE_INTERVAL, a Go duration such as "6h"). Zero
	// disables the timer entirely; the manual trigger (POST /api/improve, or
	// `hyperplane improve`) keeps working either way.
	ImproveInterval time.Duration
	// AutoLearn turns on automatic skill extraction when a delivery is accepted
	// (HYPERPLANE_AUTO_LEARN, default off; "0"/"false"/"off" disables). It gates
	// a BILLED engine call that nobody explicitly asked for, so it is a switch
	// rather than a hard-coded behaviour — and a Config built by hand (tests,
	// embedders) is off until it says otherwise.
	AutoLearn bool
	// Briefs turns on ENGINE-written outcome briefs (HYPERPLANE_BRIEFS, default
	// off; "0"/"false"/"off" disables). Switching it off does not switch briefs
	// off: the platform still writes the deterministic one, because a reader
	// with no account of their outcome is the failure the feature exists to
	// end. All the switch buys is that no billed call is made.
	Briefs bool
}

// BriefsEnvVar is the switch for engine-written outcome briefs. It is exported
// for shared configuration and tests of deterministic outcome summaries.
const BriefsEnvVar = "HYPERPLANE_BRIEFS"

// BriefsEnabled reads the brief switch from the environment, with the same
// default and the same spellings-for-"no" the Config field has.
func BriefsEnabled() bool { return envBool(BriefsEnvVar, false) }

// Load reads configuration from the environment, applying local-first defaults.
func Load() Config {
	platform := env("HYPERPLANE_PLATFORM_ROOT", mustCwd())
	return Config{
		Addr:         env("HYPERPLANE_ADDR", "127.0.0.1:4252"),
		DatabaseURL:  env("DATABASE_URL", "postgres://hyperplane:hyperplane@127.0.0.1:5432/hyperplane?sslmode=disable"),
		PlatformRoot: platform,
		TargetRoot:   env("HYPERPLANE_TARGET_ROOT", filepath.Join(platform, "workspace")),
		RunsDir:      env("HYPERPLANE_RUNS_DIR", filepath.Join(platform, "runs")),

		Token:           os.Getenv("HYPERPLANE_TOKEN"),
		ShellCmd:        env("HYPERPLANE_SHELL", defaultShell()),
		OrchestratorCmd: env("HYPERPLANE_ORCHESTRATOR_CMD", filepath.Join(platform, "scripts", "orchestrator.sh")),
		AllowedOrigins:  splitNonEmpty(os.Getenv("HYPERPLANE_ALLOWED_ORIGINS")),
		WebDir:          os.Getenv("HYPERPLANE_WEB_DIR"),
		ImproveInterval: envDuration("HYPERPLANE_IMPROVE_INTERVAL", defaultImproveInterval),
		AutoLearn:       envBool("HYPERPLANE_AUTO_LEARN", false),
		Briefs:          BriefsEnabled(),
	}
}

// envBool reads an on/off switch. Anything a person would write for "no" reads
// as false; anything else present reads as true.
func envBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// envDuration reads a Go duration from the environment. "0" (or any zero
// duration) is a deliberate OFF switch, not a missing value, so it is honoured
// rather than replaced by the default. An unparseable value falls back to the
// default and says so — silently running on a different schedule than the
// operator wrote is worse than a noisy line at boot.
func envDuration(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ config: %s=%q is not a duration (e.g. \"6h\", \"0\") — using %s\n", key, raw, def)
		return def
	}
	if d < 0 {
		return 0
	}
	return d
}

// splitNonEmpty splits a comma-separated list, trimming spaces and dropping empties.
func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// defaultShell resolves the user's login shell, falling back sensibly per-OS.
func defaultShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
