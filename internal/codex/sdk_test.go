package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSDKBridgeUsesRealSDKWithFixtureExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fixture")
	script := `#!/usr/bin/env python3
import json, sys, os
args=sys.argv[1:]
assert args[0]=='exec' and '--ignore-user-config' in args and '--ignore-rules' in args
assert args[args.index('--sandbox')+1]=='read-only'
assert '--add-dir' not in args
assert 'approval_policy="never"' in args
assert 'sandbox_workspace_write.network_access=false' in args
prompt=sys.stdin.read()
assert 'fixture request' in prompt
print(json.dumps({'type':'thread.started','thread_id':'fixture-thread'}), flush=True)
if os.environ.get('SDK_TEST_FAIL'): sys.exit(9)
print(json.dumps({'type':'item.completed','item':{'id':'answer','type':'agent_message','text':'{"answer":42}'}}))
print(json.dumps({'type':'turn.completed','usage':{'input_tokens':17,'cached_input_tokens':3,'output_tokens':2}}))
`
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", exe)
	options := Options{Instruction: "fixture request", Cwd: dir, RunDir: dir, Label: "check", SandboxMode: SandboxReadOnly}
	result, err := Run(context.Background(), options)
	if err != nil || !result.OK || string(result.JSON) != `{"answer":42}` || result.ThreadID != "fixture-thread" || result.InputTokens != 17 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "check.usage.json"))
	if err != nil || !strings.Contains(string(data), "fixture-thread") {
		t.Fatalf("missing SDK continuation receipt: %s, %v", data, err)
	}
	t.Setenv("SDK_TEST_FAIL", "1")
	result, err = Run(context.Background(), options)
	if err != nil || result.OK || result.ThreadID != "fixture-thread" {
		t.Fatalf("SDK process failure accepted: %+v %v", result, err)
	}
}

func TestSDKInterruptedStreamPreservesContinuationIdentity(t *testing.T) {
	_, id, completed := sdkResult(`{"type":"thread.started","thread_id":"interrupted-thread"}
{"type":"turn.started"}
`)
	if id != "interrupted-thread" || completed {
		t.Fatalf("id=%q completed=%v", id, completed)
	}
}

func TestAuxiliarySDKDefaultCreatesNoEvidenceFiles(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "sdk-fixture")
	if err := os.WriteFile(fixture, []byte(`#!/usr/bin/env python3
import json,sys
args=sys.argv[1:]
assert args[args.index('--sandbox')+1]=='read-only'
assert '--add-dir' not in args
prompt=sys.stdin.read()
print(json.dumps({'type':'thread.started','thread_id':'auxiliary'}))
print(json.dumps({'type':'item.completed','item':{'id':'answer','type':'agent_message','text':prompt}}))
print(json.dumps({'type':'turn.completed','usage':{'input_tokens':1,'output_tokens':1}}))
`), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", fixture)
	platformRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_PLATFORM_ROOT", platformRoot)
	workspace := t.TempDir()
	t.Chdir(workspace)
	var wg sync.WaitGroup
	for _, instruction := range []string{"first call", "second call", "third call"} {
		wg.Add(1)
		go func(instruction string) {
			defer wg.Done()
			result, err := Run(t.Context(), Options{Instruction: instruction, Cwd: workspace})
			if err != nil || !result.OK || result.Text != instruction || result.LogPath != "" || result.Sandbox != SandboxReadOnly {
				t.Errorf("default auxiliary result = %+v, error=%v", result, err)
			}
		}(instruction)
	}
	wg.Wait()
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 0 {
		t.Fatalf("auxiliary calls left files in the caller workspace: %v, %v", files, err)
	}
}

func TestAuxiliarySDKRejectsWritableCallsAndInvalidEvidenceBeforeStarting(t *testing.T) {
	// If validation reached the SDK, this deliberately missing executable would
	// produce a transport error instead of the relevant admission error.
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(t.TempDir(), "missing"))
	for _, tc := range []struct {
		options Options
		want    string
	}{
		{Options{SandboxMode: "workspace-write"}, "managed SDK task"},
		{Options{Label: "../escape"}, "filename"},
		{Options{Label: `..\escape`}, "filename"},
		{Options{RunDir: t.TempDir()}, "requires a label"},
		{Options{RunDir: filepath.Join(t.TempDir(), "missing"), Label: "call"}, "already exist"},
	} {
		if _, err := Run(t.Context(), tc.options); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("options=%+v error=%v, want %q", tc.options, err, tc.want)
		}
	}
}
