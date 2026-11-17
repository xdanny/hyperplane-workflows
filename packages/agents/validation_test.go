package agentchecks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ordinaryValidationNodes = json.RawMessage(`[{"id":"write","kind":"task","title":"Write","instruction":"Create answer.txt with 42"}]`)

func validationInstallation(t *testing.T) string {
	t.Helper()
	platform := t.TempDir()
	root := filepath.Join(platform, "packages", "agents")
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(runtimeCopy(t), root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_PLATFORM_ROOT", platform)
	return root
}

func fakeValidator(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestInspectWorkflowUsesSDKAndReportsRevision(t *testing.T) {
	validationInstallation(t)
	report, err := InspectWorkflow(t.Context(), "Write a file", ordinaryValidationNodes)
	if err != nil || !report.Valid || report.Issue != nil || report.RuntimeFingerprint != RuntimeFingerprint() || report.RuntimeFingerprintVersion != RuntimeFingerprintVersion {
		t.Fatalf("%+v %v", report, err)
	}
	// A valid command shape with incompatible owner declarations must be refused
	// by the shared SDK parser before any source file is opened or agent runs.
	raw := json.RawMessage(`[{"id":"evaluate","kind":"task","title":"Evaluate","instruction":"Compute the declared score","acceptance":"prediction-metrics","inputs":{"successMetric":"AUROC 0.75","dataDescription":"/not-present/source.csv"}}]`)
	report, err = InspectWorkflow(t.Context(), "Evaluate", raw)
	if err != nil || report.Valid || report.Issue == nil || report.Issue.NodeID != "evaluate" {
		t.Fatalf("%+v %v", report, err)
	}
}

func TestInspectWorkflowRejectsMismatchedInstallationBeforeLaunching(t *testing.T) {
	root := validationInstallation(t)
	marker := filepath.Join(t.TempDir(), "launched")
	fakeValidator(t, "echo launched > '"+marker+"'")
	p := filepath.Join(root, "src", "validate-worker.mjs")
	file, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("\n// modified installed source\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorkflow(t.Context(), "Write", ordinaryValidationNodes); !errors.Is(err, ErrValidationUnavailable) {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("validator launched before checking installation")
	}
}

func TestInspectWorkflowUnavailableAndCancelled(t *testing.T) {
	t.Run("missing installation", func(t *testing.T) {
		t.Setenv("HYPERPLANE_PLATFORM_ROOT", t.TempDir())
		if _, err := InspectWorkflow(t.Context(), "Write", ordinaryValidationNodes); !errors.Is(err, ErrValidationUnavailable) {
			t.Fatal(err)
		}
	})
	t.Run("missing Node", func(t *testing.T) {
		validationInstallation(t)
		t.Setenv("PATH", t.TempDir())
		if _, err := InspectWorkflow(t.Context(), "Write", ordinaryValidationNodes); !errors.Is(err, ErrValidationUnavailable) {
			t.Fatal(err)
		}
	})
	t.Run("caller cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := InspectWorkflow(ctx, "Write", ordinaryValidationNodes); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrValidationUnavailable) {
			t.Fatal(err)
		}
	})
	t.Run("running process deadline", func(t *testing.T) {
		validationInstallation(t)
		fakeValidator(t, "exec /bin/sleep 30")
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		before := time.Now()
		if _, err := InspectWorkflow(ctx, "Write", ordinaryValidationNodes); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrValidationUnavailable) {
			t.Fatal(err)
		}
		if time.Since(before) > 2*time.Second {
			t.Fatal("deadline did not terminate validator")
		}
	})
}

func TestInspectWorkflowRefusesUntrustedProtocol(t *testing.T) {
	good := `{"valid":true,"runtimeFingerprint":"` + RuntimeFingerprint() + `","runtimeFingerprintVersion":1}`
	for name, payload := range map[string]string{
		"empty": "", "malformed": "{", "missing verdict": strings.Replace(good, `"valid":true,`, "", 1),
		"missing issue":     strings.Replace(good, `"valid":true`, `"valid":false`, 1),
		"wrong fingerprint": strings.Replace(good, RuntimeFingerprint(), strings.Repeat("0", 64), 1),
		"wrong version":     strings.Replace(good, `Version":1`, `Version":2`, 1),
		"trailing result":   good + good,
		"unknown field":     strings.Replace(good, `"valid":true`, `"valid":true,"unsafe":true`, 1),
		"oversize":          strings.Repeat(" ", validationLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			validationInstallation(t)
			output := filepath.Join(t.TempDir(), "response")
			if err := os.WriteFile(output, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			fakeValidator(t, "exec /bin/cat '"+output+"'")
			if _, err := InspectWorkflow(t.Context(), "Write", ordinaryValidationNodes); !errors.Is(err, ErrValidationUnavailable) {
				t.Fatal(err)
			}
		})
	}
	t.Run("nonzero status with forged success", func(t *testing.T) {
		validationInstallation(t)
		fakeValidator(t, "echo '"+good+"'\nexit 1")
		if _, err := InspectWorkflow(t.Context(), "Write", ordinaryValidationNodes); !errors.Is(err, ErrValidationUnavailable) {
			t.Fatal(err)
		}
	})
}

func TestInspectWorkflowBoundsRequestBeforeProcess(t *testing.T) {
	validationInstallation(t)
	marker := filepath.Join(t.TempDir(), "launched")
	fakeValidator(t, "echo launched > '"+marker+"'")
	report, err := InspectWorkflow(t.Context(), strings.Repeat("x", validationLimit), ordinaryValidationNodes)
	if err != nil || report.Valid || report.Issue == nil {
		t.Fatalf("%+v %v", report, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("oversize input launched process")
	}
}
