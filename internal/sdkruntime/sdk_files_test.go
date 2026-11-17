package sdkruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/codex"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

func TestSDKWorkFilesStreamLargeApprovalAndDelivery(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "build"), 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "build", "large.csv")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 * 1024 * 1024
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	file.Close()
	hash := sha256.New()
	chunk := make([]byte, 256*1024)
	for bytes := 0; bytes < size; bytes += len(chunk) {
		hash.Write(chunk)
	}
	req := codex.WorkflowRequest{Workspace: root, Nodes: []sdkworkflow.Node{{ID: "build", Kind: "task"}, {ID: "approve", Kind: "approval"}}}
	raw, _ := json.Marshal(map[string]any{"nodes": []any{map[string]any{"id": "build", "files": []any{map[string]any{"path": "build/large.csv", "bytes": size, "sha256": fmt.Sprintf("%x", hash.Sum(nil))}}}}})
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	files, err := sdkWorkFiles(context.Background(), req, raw, "approve", "test-run")
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0]["bytes"] != "67108864" || !strings.Contains(files[0]["url"], "large.csv") {
		t.Fatalf("missing large approval file: %+v", files)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8*1024*1024 {
		t.Fatalf("large file inspection allocated %d bytes", allocated)
	}
	t.Logf("Inspected %d bytes; Go allocations during inspection: %d bytes", size, after.TotalAlloc-before.TotalAlloc)
	if _, err := sdkWorkFiles(context.Background(), req, raw, "", "test-run"); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "build", "linked.csv")
	if err := os.Link(name, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := sdkWorkFiles(context.Background(), req, raw, "approve", "test-run"); err == nil {
		t.Fatal("hardlinked artifact accepted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	file, err = os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt([]byte("tampered"), size-8)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdkWorkFiles(context.Background(), req, raw, "approve", "test-run"); err == nil {
		t.Fatal("changed large file retained approval")
	}
}

// Trigger cancellation after inspection has started, independent of disk speed.
type cancelDuringInspection struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelDuringInspection) Err() error {
	c.checks++
	if c.checks == 4 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSDKWorkFilesCancelDuringStream(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "data.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(8 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	file.Close()
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlled := &cancelDuringInspection{Context: ctx, cancel: cancel}
	if _, err := hashSDKWorkFile(controlled, handle, "data.csv", 8*1024*1024, make([]byte, 256*1024)); !errors.Is(err, context.Canceled) {
		t.Fatalf("stream cancellation: %v", err)
	}
	if controlled.checks != 4 {
		t.Fatalf("continued after cancellation: %d checks", controlled.checks)
	}
}

func TestSDKWorkFilesRejectQuotaBeforeOpeningAndHonorCancellation(t *testing.T) {
	req := codex.WorkflowRequest{Workspace: t.TempDir(), Nodes: []sdkworkflow.Node{{ID: "build", Kind: "task"}}}
	entries := []any{}
	for i := 0; i < 5; i++ {
		entries = append(entries, map[string]any{"path": fmt.Sprintf("build/%d.csv", i), "bytes": sdkMaxFileBytes, "sha256": strings.Repeat("0", 64)})
	}
	raw, _ := json.Marshal(map[string]any{"nodes": []any{map[string]any{"id": "build", "files": entries}}})
	if _, err := sdkWorkFiles(context.Background(), req, raw, "", "test"); err == nil || !strings.Contains(err.Error(), "8 GB") {
		t.Fatalf("quota must precede file opens: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sdkWorkFiles(ctx, req, raw, "", "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	// The same constants govern standalone SDK consumers and application approval.
	source, err := os.ReadFile("../../packages/agents/src/workflows/files.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []string{"maxFileBytes = 2_000_000_000", "maxStepBytes = 8_000_000_000"} {
		if !strings.Contains(string(source), declaration) {
			t.Fatalf("SDK/host quota mismatch: %s", declaration)
		}
	}
}
