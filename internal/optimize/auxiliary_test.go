package optimize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuxiliarySDKCallsOwnAndCleanPrivateWorkspaces(t *testing.T) {
	fixture, err := filepath.Abs("testdata/auxiliary")
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", fixture)
	t.Setenv("SDK_AUXILIARY_CALLS", ledger)
	call := func(ctx context.Context, proposal bool) error {
		if proposal {
			_, err := proposeVariants(ctx, "codex", "Instruction", makeEvalset("cases", "one"), nil, 1, "")
			return err
		}
		_, _, err := (&codexGenEngine{}).runRaw(ctx, "Generate case inputs")
		return err
	}
	for _, mode := range []string{"pass", "fail", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SDK_AUXILIARY_MODE", mode)
			var wg sync.WaitGroup
			failures := make(chan error, 4)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func(proposal bool) {
					defer wg.Done()
					failures <- call(ctx, proposal)
				}(i%2 == 0)
			}
			if mode == "hang" {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					raw, _ := os.ReadFile(ledger)
					if strings.Count(string(raw), "\n") == 12 {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				if mode == "pass" && err != nil {
					t.Fatal(err)
				}
				if mode != "pass" && err == nil {
					t.Fatal("failed SDK call succeeded")
				}
			}
		})
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 12 {
		t.Fatalf("expected 12 calls, got %d: %s", len(lines), raw)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var record struct{ Workspace string }
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Workspace == "" || seen[record.Workspace] {
			t.Fatalf("shared workspace: %+v", record)
		}
		seen[record.Workspace] = true
		if _, err := os.Stat(record.Workspace); !os.IsNotExist(err) {
			t.Fatalf("call left private files after success/failure/cancel: %s %v", record.Workspace, err)
		}
	}
}
