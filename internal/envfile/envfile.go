// Package envfile is a zero-dependency loader for a local, gitignored .env file.
// It exists so local secrets (e.g. CONTEXT7_API_KEY) are picked up by the server
// and CLI automatically, without the user having to export them in every shell.
// Real environment variables always win — Load never overwrites an existing var.
package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Load reads KEY=VALUE lines from path and sets any variable not already present
// in the process environment. Best-effort and silent: a missing file is not an
// error (the .env is optional), and malformed lines are skipped. Lines may be
// blank or start with '#'. Values may be optionally quoted.
func Load(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // no local .env — fine
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		// A real exported env var takes precedence over the file.
		if _, exists := os.LookupEnv(k); k != "" && !exists {
			_ = os.Setenv(k, v)
		}
	}
}
