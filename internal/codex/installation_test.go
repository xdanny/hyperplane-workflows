package codex

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallationUsesPackagedSDKWithoutGlobalCodex(t *testing.T) {
	// Expose just Node and dirname. In particular there is no global codex.
	bin := t.TempDir()
	for _, name := range []string{"node", "dirname"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", "")
	if err := CheckInstallation(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestInstallationRejectsMissingDependencyAndRuntimeMismatch(t *testing.T) {
	directory, err := SDKDirectory()
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Dir(directory)
	inventoryJSON, err := os.ReadFile(filepath.Join(installed, "runtime-files.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct{ Files []string }
	if err := json.Unmarshal(inventoryJSON, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-dependency", "changed-runtime"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			pkg := filepath.Join(root, "packages", "agents")
			for _, name := range inventory.Files {
				data, err := os.ReadFile(filepath.Join(installed, name))
				if err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(pkg, name)
				if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, data, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "changed-runtime" {
				if err := os.WriteFile(filepath.Join(pkg, "src/index.mjs"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HYPERPLANE_PLATFORM_ROOT", root)
			// A global codex is irrelevant even when it exists and exits successfully.
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := CheckInstallation(t.Context()); err == nil {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}

func TestInstallationHonorsConfiguredExecutableWithoutRunningWork(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "codex-override")
	// Any invocation is a failure: overrides can be executable model fixtures.
	script := "#!/bin/sh\nexit 91\n"
	if err := os.WriteFile(override, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", override)
	if err := CheckInstallation(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(override); err != nil {
		t.Fatal(err)
	}
	if err := CheckInstallation(t.Context()); err == nil {
		t.Fatal("ignored missing configured executable")
	}
}
