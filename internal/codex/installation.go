package codex

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	agentchecks "github.com/xdanny/hyperplane-workflows/packages/agents"
)

// CheckInstallation checks the same SDK source, dependency import and launcher
// used for execution. It does not start a thread or inspect authentication/model
// access. Errors deliberately omit paths, configuration and subprocess output.
func CheckInstallation(ctx context.Context) error {
	directory, err := SDKDirectory()
	if err != nil {
		return fmt.Errorf("SDK installation is unavailable")
	}
	fingerprint, err := agentchecks.ReadRuntimeFingerprint(filepath.Dir(directory))
	if err != nil || fingerprint != agentchecks.RuntimeFingerprint() {
		return fmt.Errorf("SDK installation does not match this backend")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Import by absolute module path without changing the inherited execution cwd.
	// The launcher checks overrides without invoking them and uses the official
	// package's version command otherwise; no conversation or tool can start.
	const probe = `import { pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
const { workflowCodex } = await import(pathToFileURL(process.argv[1]));
workflowCodex();
execFileSync(process.argv[2], ['--check-installation'], { stdio: 'ignore', timeout: 1500 });`
	cmd := ownedCommand(ctx, "node", "--input-type=module", "--eval", probe,
		filepath.Join(directory, "index.mjs"), filepath.Join(filepath.Dir(directory), "bin", "codex-isolated"))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SDK dependency or executable is unavailable")
	}
	return nil
}
