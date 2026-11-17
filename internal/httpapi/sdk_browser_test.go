package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Opt-in because Chromium and web dependencies are not requirements of the
// native Go gate. No application HTTP routes or SDK persistence are mocked.
func TestSDKBrowserJourney(t *testing.T) {
	if os.Getenv("HYPERPLANE_BROWSER_JOURNEY") != "1" {
		t.Skip("opt-in real browser journey: set HYPERPLANE_BROWSER_JOURNEY=1 and TEST_DATABASE_URL")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Fatal("the browser journey requires explicit TEST_DATABASE_URL")
	}
	if err := dbtest.GuardTestDSN(os.Getenv("DATABASE_URL")); err != nil {
		t.Fatal(err)
	}
	root := findRepoRoot(t)
	web := filepath.Join(root, "apps", "web")
	artifacts := t.TempDir()
	if requested := os.Getenv("HYPERPLANE_BROWSER_ARTIFACTS"); requested != "" {
		var err error
		artifacts, err = filepath.Abs(requested)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(artifacts, 0700); err != nil {
			t.Fatal(err)
		}
	}
	calls := filepath.Join(artifacts, "sdk-calls.jsonl")
	if err := os.WriteFile(calls, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDK_FIXTURE_BROWSER_CALLS", calls)
	source := filepath.Join(t.TempDir(), "sales.csv")
	sourceData := []byte("region,amount\nnorth,7\nsouth,11\n")
	if err := os.WriteFile(source, sourceData, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDK_FIXTURE_BROWSER_SOURCE", source)
	connectionValue := "browser-only-connection-value-" + filepath.Base(filepath.Dir(source))
	t.Setenv("WORKFLOW_BROWSER_SOURCE_TOKEN", connectionValue)
	t.Setenv("WORKFLOW_BROWSER_UNSELECTED", "unselected-test-value")
	t.Setenv("WORKFLOW_BROWSER_MISSING", "")
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(web, "e2e-live", "fixtures", "sdk-agent"))
	t.Setenv("HYPERPLANE_MODEL", "fixture-model")

	// Pick a fresh loopback port, never the user's standard API or preview.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	browserURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	_, api := newTestServer(t, func(cfg *config.Config) {
		cfg.AllowedOrigins = []string{browserURL}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	build := browserOwnedCommand(ctx, "npm", "run", "build")
	build.Dir = web
	if output, err := browserCombinedOutput(build); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	previewLog, err := os.Create(filepath.Join(artifacts, "preview.log"))
	if err != nil {
		t.Fatal(err)
	}
	preview := browserOwnedCommand(ctx, "node", "node_modules/vite/bin/vite.js", "preview", "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--strictPort")
	preview.Dir = web
	preview.Env = append(os.Environ(), "VITE_API_BASE="+api.URL)
	preview.Stdout, preview.Stderr = previewLog, previewLog
	if err = preview.Start(); err != nil {
		previewLog.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Signal the owned group, allowing Vite to close its children.
		_ = preview.Cancel()
		_ = preview.Wait()
		_ = syscall.Kill(-preview.Process.Pid, syscall.SIGKILL)
		_ = previewLog.Close()
	})
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, err := client.Get(browserURL + "/api/health")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated preview did not proxy the real API")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("real API %s; isolated preview %s; artifacts %s", api.URL, browserURL, artifacts)
	receiptPath := filepath.Join(artifacts, "browser-receipt.json")
	browser := browserOwnedCommand(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "playwright.live.config.ts")
	browser.Dir = web
	browser.Env = append(os.Environ(), "SDK_BROWSER_API="+api.URL, "SDK_BROWSER_URL="+browserURL,
		"SDK_BROWSER_RECEIPT="+receiptPath, "SDK_BROWSER_CONNECTED_RECEIPT="+filepath.Join(artifacts, "connected-receipt.json"), "SDK_BROWSER_OUTPUT="+filepath.Join(artifacts, "browser"))
	output, err := browserCombinedOutput(browser)
	t.Logf("%s", output)
	if err != nil {
		t.Fatalf("real browser journey: %v", err)
	}
	var receipt struct {
		WorkflowID    string             `json:"workflowId"`
		RunID         string             `json:"runId"`
		Revision      int64              `json:"revision"`
		Nodes         []sdkworkflow.Node `json:"nodes"`
		DecisionID    string             `json:"decisionId"`
		ProposedNodes []sdkworkflow.Node `json:"proposedNodes"`
		Authoring     map[string]bool    `json:"authoring"`
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil || json.Unmarshal(data, &receipt) != nil {
		t.Fatalf("browser did not leave a valid receipt: %v", err)
	}
	for _, path := range []string{"descriptions", "goalProposal", "proposalReviewedBeforeApply", "paletteDrag", "reorderDrag", "clickToAdd"} {
		if !receipt.Authoring[path] {
			t.Fatalf("missing authoring evidence: %s", path)
		}
	}
	if len(receipt.ProposedNodes) != 2 || len(receipt.Nodes) != 4 || receipt.Nodes[0].ID != receipt.ProposedNodes[0].ID || !reflect.DeepEqual(receipt.Nodes[1], receipt.ProposedNodes[1]) {
		t.Fatal("saved workflow lost the applied proposal's node identity or unchanged file step")
	}
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	workflows, err := st.ListSDKWorkflows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, workflow := range workflows {
		if workflow.ID == receipt.WorkflowID {
			found = workflow.Revision == 2 && reflect.DeepEqual(workflow.Nodes, receipt.Nodes)
		}
	}
	if !found {
		t.Fatal("saved revision/nodes differ from the browser's reviewed definition")
	}
	run, err := st.GetRun(ctx, receipt.RunID)
	if err != nil || run.State != "passed" || !bytes.Contains(run.Doc, []byte(`"not_requested"`)) {
		t.Fatalf("missing completed unchecked task checkpoint: %s %v", run.Doc, err)
	}
	decision, err := st.GetDecision(ctx, receipt.DecisionID)
	if err != nil || decision.Decision != "accepted" || decision.Why != "Reviewed fixture outputs and their explicit limits." {
		t.Fatalf("human decision not persisted: %+v %v", decision, err)
	}
	ledger, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(string(ledger)), "\n") {
		var call struct {
			Kind, Runtime                                 string
			SelectedPresent, UnselectedAbsent, SourceRead bool
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, call.Kind)
		if call.Kind == "connected-task" {
			if !call.SelectedPresent || !call.UnselectedAbsent || !call.SourceRead {
				t.Fatal("connected SDK task did not verify its source and environment boundary")
			}
			if _, err := os.Stat(call.Runtime); !os.IsNotExist(err) {
				t.Fatalf("connected task temporary runtime remains: %v", err)
			}
		}
	}
	if !reflect.DeepEqual(kinds, []string{"connected-task", "plan", "document", "task", "review"}) {
		t.Fatalf("unexpected SDK turns (approval/outcome must not call the model): %v", kinds)
	}

	var connected struct {
		WorkflowID, RunID string
		Revision          int64
		Nodes             []sdkworkflow.Node
		FailedRunIDs      []string
	}
	connectedData, err := os.ReadFile(filepath.Join(artifacts, "connected-receipt.json"))
	if err != nil || json.Unmarshal(connectedData, &connected) != nil {
		t.Fatal("missing connected browser receipt")
	}
	var savedConnected store.SDKWorkflow
	for _, workflow := range workflows {
		if workflow.ID == connected.WorkflowID {
			savedConnected = workflow
		}
	}
	if savedConnected.ID == "" || savedConnected.Revision != connected.Revision || !reflect.DeepEqual(savedConnected.Nodes, connected.Nodes) {
		t.Fatal("connected saved settings differ from reviewed browser settings")
	}
	if len(connected.Nodes) != 1 || connected.Nodes[0].Access == nil || !reflect.DeepEqual(connected.Nodes[0].Access, &sdkworkflow.TaskAccess{Hosts: []string{"source.example.invalid"}, ReadPaths: []string{source}, Environment: []string{"WORKFLOW_BROWSER_SOURCE_TOKEN"}}) {
		t.Fatal("connected access settings were not preserved")
	}
	if len(connected.FailedRunIDs) != 2 {
		t.Fatal("missing source and connection preflight evidence")
	}
	for _, id := range append(connected.FailedRunIDs, connected.RunID) {
		connectedRun, err := st.GetRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := "failed"
		if id == connected.RunID {
			want = "passed"
		}
		if connectedRun.State != want {
			t.Fatalf("connected run state=%s want=%s", connectedRun.State, want)
		}
		encoded, _ := json.Marshal(connectedRun)
		if bytes.Contains(encoded, []byte(connectionValue)) {
			t.Fatal("connection value leaked into persisted run or API fields")
		}
	}
	storedSource, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(storedSource, sourceData) {
		t.Fatal("source input was modified")
	}
	for _, candidate := range [][]byte{connectedData, data, ledger, output} {
		if bytes.Contains(candidate, []byte(connectionValue)) {
			t.Fatal("connection value leaked into browser or SDK evidence")
		}
	}
}

// The repository supports macOS and Linux. Signal the whole owned group first:
// Playwright handles SIGTERM by closing its separately grouped Chromium process.
// A bounded fallback prevents unresponsive build/runner descendants retaining pipes.
func browserOwnedCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func browserCombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	output, err := cmd.CombinedOutput()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return output, err
}

func TestSDKBrowserOwnedCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := browserOwnedCommand(ctx, "sh", "-c", `trap 'wait; exit' TERM
sh -c 'trap "echo child-stopped; exit" TERM; echo ready; while :; do sleep 1; done' &
wait`)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("child did not start")
	}
	cancel()
	var output []string
	for scanner.Scan() {
		output = append(output, scanner.Text())
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled command unexpectedly succeeded")
	}
	if !strings.Contains(strings.Join(output, "\n"), "child-stopped") {
		t.Fatalf("child did not receive cancellation: %v", output)
	}
}
