package agentchecks

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func runtimeCopy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := fs.WalkDir(runtimeSource, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := runtimeSource.ReadFile(name)
		if err != nil {
			return err
		}
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func jsFingerprint(t *testing.T, root string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required to verify Go/JS runtime fingerprint parity")
	}
	// Execute the trusted reader from this checkout against candidate package bytes.
	command := exec.Command(node, "--input-type=module", "-e", `import {runtimeFingerprint} from './src/runtime.mjs'; console.log(await runtimeFingerprint({root:process.argv[1]}));`, root)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("JS fingerprint: %s %v", output, err)
	}
	return strings.TrimSpace(string(output))
}

func TestRuntimeFingerprintParityAndSourceIdentity(t *testing.T) {
	root := runtimeCopy(t)
	expected := RuntimeFingerprint()
	got, err := ReadRuntimeFingerprint(root)
	if err != nil || got != expected {
		t.Fatalf("embedded/disk: %s %s %v", expected, got, err)
	}
	if got := jsFingerprint(t, root); got != expected {
		t.Fatalf("Go/JS mismatch: %s %s", expected, got)
	}
	for _, name := range []string{"README.md", "test/irrelevant.test.mjs", "node_modules/irrelevant/index.js"} {
		destination := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte("irrelevant"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := ReadRuntimeFingerprint(root); err != nil || got != expected {
		t.Fatalf("excluded files affected Go: %s %v", got, err)
	}
	if got := jsFingerprint(t, root); got != expected {
		t.Fatal("excluded files affected JS")
	}
	for _, name := range []string{"src/turn.mjs", "checks.json", "package-lock.json", "bin/codex-isolated"} {
		location := filepath.Join(root, name)
		original, err := os.ReadFile(location)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(location, append(original, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		changed, err := ReadRuntimeFingerprint(root)
		if err != nil || changed == expected {
			t.Fatalf("runtime change not detected: %s %v", name, err)
		}
		if changed != jsFingerprint(t, root) {
			t.Fatalf("changed runtime parity: %s", name)
		}
		if err := os.WriteFile(location, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeInventoryExactlyCoversShippedSources(t *testing.T) {
	raw, _ := runtimeSource.ReadFile("runtime-files.json")
	names, err := runtimeInventory(raw)
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	if err := fs.WalkDir(runtimeSource, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			actual = append(actual, name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(names, actual) {
		t.Fatalf("inventory drift:\ninventory %v\nembedded %v", names, actual)
	}
	// A production source added beyond the embed patterns must not silently disappear.
	for _, directory := range []string{"src", "bin"} {
		if err := filepath.WalkDir(directory, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if directory == "src" && !strings.HasSuffix(name, ".mjs") && !strings.HasSuffix(name, ".py") {
				return nil
			}
			i := sort.SearchStrings(names, filepath.ToSlash(name))
			if i == len(names) || names[i] != filepath.ToSlash(name) {
				t.Errorf("unlisted production source: %s", name)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeInventoryRejectsInvalidPathsAndBounds(t *testing.T) {
	raw, _ := runtimeSource.ReadFile("runtime-files.json")
	names, _ := runtimeInventory(raw)
	for _, invalid := range []string{"../secret", "/etc/passwd", "src/../package.json", "src/index.mjs\n", "src\\index.mjs", "test/x.mjs", "node_modules/x.mjs", "src/nested/x.mjs"} {
		candidate := append(append([]string{}, names...), invalid)
		sort.Strings(candidate)
		data, _ := json.Marshal(map[string]any{"version": 1, "files": candidate})
		if _, err := runtimeInventory(data); err == nil {
			t.Fatalf("accepted path %q", invalid)
		}
	}
	for _, candidate := range [][]string{append(append([]string{}, names...), names[0]), names[1:], make([]string, 257)} {
		data, _ := json.Marshal(map[string]any{"version": 1, "files": candidate})
		if _, err := runtimeInventory(data); err == nil {
			t.Fatal("accepted invalid inventory")
		}
	}
	root := runtimeCopy(t)
	target := filepath.Join(root, "src/turn.mjs")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "src/index.mjs"), target); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeFingerprint(root); err == nil {
		t.Fatal("accepted source symlink")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, make([]byte, runtimeFileLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeFingerprint(root); err == nil {
		t.Fatal("accepted oversized source")
	}
	_, err := fingerprintRuntime(func(name string) ([]byte, error) {
		if name == "runtime-files.json" {
			return raw, nil
		}
		return make([]byte, runtimeFileLimit), nil
	})
	if err == nil {
		t.Fatal("accepted oversized runtime")
	}
}
