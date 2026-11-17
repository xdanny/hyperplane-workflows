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

func TestContinuationTransportBoundsAndCancellation(t *testing.T) {
	request := json.RawMessage(`{"instruction":"Write","nodes":[]}`)
	t.Run("oversize checkpoint never starts worker", func(t *testing.T) {
		validationInstallation(t)
		marker := filepath.Join(t.TempDir(), "launched")
		fakeValidator(t, "echo launched > '"+marker+"'")
		checkpoint, _ := json.Marshal(strings.Repeat("x", validationLimit))
		_, err := ReviewContinuation(t.Context(), request, checkpoint, t.TempDir())
		if !errors.Is(err, ErrContinuationBlocked) {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("oversize record started worker")
		}
	})
	t.Run("uncooperative worker terminates", func(t *testing.T) {
		validationInstallation(t)
		fakeValidator(t, "trap '' TERM\nexec /bin/sleep 30")
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		before := time.Now()
		_, err := PrepareContinuation(ctx, request, json.RawMessage(`{}`), t.TempDir(), t.TempDir())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if time.Since(before) > 3*time.Second {
			t.Fatal("continuation worker outlived cancellation deadline")
		}
	})
}

func TestContinuationTransportRejectsUnboundResponses(t *testing.T) {
	identity := `,"runtimeFingerprint":"` + RuntimeFingerprint() + `","runtimeFingerprintVersion":1}`
	for name, response := range map[string]string{
		"wrong identity":  `{"ok":false,"reason":"blocked","runtimeFingerprint":"` + strings.Repeat("0", 64) + `","runtimeFingerprintVersion":1}`,
		"missing plan":    `{"ok":true` + identity,
		"missing reason":  `{"ok":false` + identity,
		"trailing result": `{"ok":false,"reason":"blocked"` + identity + `{}`,
		"oversize":        strings.Repeat(" ", validationLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			validationInstallation(t)
			output := filepath.Join(t.TempDir(), "response")
			if err := os.WriteFile(output, []byte(response), 0600); err != nil {
				t.Fatal(err)
			}
			fakeValidator(t, "exec /bin/cat '"+output+"'")
			_, err := ReviewContinuation(t.Context(), json.RawMessage(`{}`), json.RawMessage(`{}`), t.TempDir())
			if !errors.Is(err, ErrValidationUnavailable) {
				t.Fatal(err)
			}
		})
	}
}
