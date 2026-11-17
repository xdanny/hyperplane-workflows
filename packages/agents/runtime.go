package agentchecks

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

const RuntimeFingerprintVersion = 1
const runtimeFileLimit = 2_000_000
const runtimeTotalLimit = 16_000_000

//go:embed runtime-files.json checks.json package.json package-lock.json bin/codex-isolated src/*.mjs src/workflows/*.mjs src/workflows/*.py
var runtimeSource embed.FS

// RuntimeFingerprint identifies the SDK sources and dependency manifests built
// into this backend. It does not attest installed dependencies or host binaries.
func RuntimeFingerprint() string {
	value, err := fingerprintRuntime(runtimeSource.ReadFile)
	if err != nil {
		panic(fmt.Errorf("invalid embedded SDK runtime: %w", err))
	}
	return value
}

// ReadRuntimeFingerprint identifies an installed SDK package using its bounded,
// explicit inventory. Callers compare this with RuntimeFingerprint before launch.
func ReadRuntimeFingerprint(root string) (string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	return fingerprintRuntime(func(name string) ([]byte, error) {
		// Root confines all reads, including a concurrent ancestor-link replacement.
		// Reject static links too, so an installation cannot depend on external files.
		parts := strings.Split(name, "/")
		for i := range parts {
			info, err := dir.Lstat(strings.Join(parts[:i+1], "/"))
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("SDK runtime link: %s", name)
			}
		}
		before, err := dir.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !before.Mode().IsRegular() || before.Size() > runtimeFileLimit {
			return nil, fmt.Errorf("invalid SDK runtime file: %s", name)
		}
		file, err := dir.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > runtimeFileLimit || !os.SameFile(before, info) {
			return nil, fmt.Errorf("invalid SDK runtime file: %s", name)
		}
		data, err := io.ReadAll(io.LimitReader(file, runtimeFileLimit+1))
		if err != nil {
			return nil, err
		}
		after, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return nil, fmt.Errorf("SDK runtime file changed: %s", name)
		}
		return data, nil
	})
}

var runtimePath = regexp.MustCompile(`^(runtime-files\.json|checks\.json|package(?:-lock)?\.json|bin/codex-isolated|src/[a-z][a-z0-9-]*\.mjs|src/workflows/[a-z][a-z0-9_-]*\.(?:mjs|py))$`)

func runtimeInventory(data []byte) ([]string, error) {
	var inventory struct {
		Version int      `json:"version"`
		Files   []string `json:"files"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF || inventory.Version != RuntimeFingerprintVersion || len(inventory.Files) == 0 || len(inventory.Files) > 256 {
		return nil, fmt.Errorf("invalid SDK runtime inventory")
	}
	for i, name := range inventory.Files {
		if !fs.ValidPath(name) || path.Clean(name) != name || !runtimePath.MatchString(name) || (i > 0 && inventory.Files[i-1] >= name) {
			return nil, fmt.Errorf("invalid SDK runtime inventory path: %s", name)
		}
	}
	for _, required := range []string{"runtime-files.json", "checks.json", "package.json", "package-lock.json", "bin/codex-isolated", "src/runtime.mjs"} {
		index := sort.SearchStrings(inventory.Files, required)
		if index == len(inventory.Files) || inventory.Files[index] != required {
			return nil, fmt.Errorf("missing SDK runtime inventory path: %s", required)
		}
	}
	return inventory.Files, nil
}

func fingerprintRuntime(read func(string) ([]byte, error)) (string, error) {
	inventory, err := read("runtime-files.json")
	if err != nil {
		return "", err
	}
	if len(inventory) > runtimeFileLimit {
		return "", fmt.Errorf("SDK runtime inventory too large")
	}
	names, err := runtimeInventory(inventory)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "hyperplane-sdk-runtime-v1\n")
	total := 0
	for _, name := range names {
		data := inventory
		if name != "runtime-files.json" {
			data, err = read(name)
			if err != nil {
				return "", err
			}
		}
		total += len(data)
		if len(data) > runtimeFileLimit || total > runtimeTotalLimit {
			return "", fmt.Errorf("SDK runtime exceeds fingerprint bounds")
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%x\n", name, sha256.Sum256(data))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
