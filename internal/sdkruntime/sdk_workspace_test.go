package sdkruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSDKWorkspaceLocksExcludeOverlapsAndRelease(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	child := filepath.Join(a, "child")
	for _, p := range []string{child, b} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := lockTaskWorkspace(a)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, p := range []string{a, root, child} {
		release, err := lockTaskWorkspace(p)
		if err == nil {
			release()
			t.Fatalf("overlap accepted: %s", p)
		}
	}
	release, err := lockTaskWorkspace(b)
	if err != nil {
		t.Fatal(err)
	}
	release()
	unlock()
	release, err = lockTaskWorkspace(child)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestSDKTaskRefusesRunRecordOverlap(t *testing.T) {
	for _, inside := range []bool{true, false} {
		root := t.TempDir()
		nested := filepath.Join(root, "nested")
		os.Mkdir(nested, 0700)
		target, runs := root, nested
		if inside {
			target, runs = nested, root
		}
		_, err := RunCheckedTask(context.Background(), nil, Options{RunsDir: runs, TargetRoot: target}, "test", []string{"true"})
		if err == nil {
			t.Fatal("run-record overlap accepted")
		}
	}
}
