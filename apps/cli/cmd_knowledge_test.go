package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func knowledgeCLIStore(t *testing.T) *store.Store {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("test database not configured")
	}
	st, err := store.Open(t.Context(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

func knowledgeCLIOutput(t *testing.T, command func() int) (int, []byte) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout-")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	var code int
	func() {
		os.Stdout = file
		defer func() { os.Stdout = original }()
		code = command()
	}()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, output
}

func knowledgeCLISave(t *testing.T, runID, target string) string {
	t.Helper()
	code, output := knowledgeCLIOutput(t, func() int {
		return cmdKnowledgeSave([]string{runID, "--target", target, "--json"}, true)
	})
	var result struct{ RunID, Path, Status string }
	if code != 0 || json.Unmarshal(output, &result) != nil || result.RunID != runID || result.Status != "saved" || result.Path == "" {
		t.Fatalf("knowledge save exit=%d output=%s", code, output)
	}
	return result.Path
}

type knowledgeCLIReceipt struct {
	RunID        string          `json:"runId"`
	Request      json.RawMessage `json:"request"`
	Checkpoint   json.RawMessage `json:"checkpoint"`
	HumanOutcome *store.Decision `json:"humanOutcome"`
}

func knowledgeCLIRead(t *testing.T, folder string) ([]byte, knowledgeCLIReceipt) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(folder, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt knowledgeCLIReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	return data, receipt
}

func TestKnowledgeSaveExportsActualSDKTaskReceiptWithoutInventingVerification(t *testing.T) {
	st := knowledgeCLIStore(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYPERPLANE_CODEX_EXECUTABLE", filepath.Join(root, "internal/codex/testdata/task"))
	t.Setenv("HYPERPLANE_RUNS_DIR", t.TempDir())
	t.Setenv("SDK_FIXTURE_MODE", "recovery")
	workspace := t.TempDir()
	code, output := knowledgeCLIOutput(t, func() int {
		return cmdTask([]string{"--target", workspace, "--json", "Write an answer without an optional acceptance check"})
	})
	var launched struct{ RunID, State string }
	if code != 0 || json.Unmarshal(output, &launched) != nil || launched.State != "passed" || launched.RunID == "" {
		t.Fatalf("SDK task exit=%d output=%s", code, output)
	}
	t.Cleanup(func() { _ = st.DeleteRun(context.Background(), launched.RunID) })
	answer, err := os.ReadFile(filepath.Join(workspace, "answer.txt"))
	if err != nil || string(answer) != "wrong\n" {
		t.Fatalf("fixture should complete without the omitted correctness check: %q %v", answer, err)
	}
	run, err := st.GetRun(t.Context(), launched.RunID)
	if err != nil {
		t.Fatal(err)
	}
	decisionID := store.OutcomeDecisionID(run.RunID)
	if _, err := st.CreateDecision(t.Context(), store.Decision{ID: decisionID, Title: "Review this result", Decision: "changes_requested", Why: "The answer needs correction", Tags: []string{store.OutcomeTag}, Refs: []string{"run:" + run.RunID}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), decisionID) })
	target := t.TempDir()
	gitDir := filepath.Join(target, ".git")
	if err := os.Mkdir(gitDir, 0700); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(gitDir, "index")
	if err := os.WriteFile(index, []byte("pre-existing staged work"), 0600); err != nil {
		t.Fatal(err)
	}
	folder := knowledgeCLISave(t, run.RunID, target)
	before, receipt := knowledgeCLIRead(t, folder)
	if receipt.HumanOutcome == nil || receipt.HumanOutcome.ID != decisionID || receipt.HumanOutcome.Decision != "changes_requested" || !bytes.Contains(receipt.Checkpoint, []byte(`"not_requested"`)) {
		t.Fatalf("export changed evidence meaning: %s", before)
	}
	var launch struct{ Request json.RawMessage }
	var doc struct{ SDK json.RawMessage }
	_ = json.Unmarshal(run.Options, &launch)
	_ = json.Unmarshal(run.Doc, &doc)
	for _, pair := range [][2]json.RawMessage{{receipt.Request, launch.Request}, {receipt.Checkpoint, doc.SDK}} {
		var actual, expected any
		_ = json.Unmarshal(pair[0], &actual)
		_ = json.Unmarshal(pair[1], &expected)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("saved SDK snapshot drifted: %s versus %s", pair[0], pair[1])
		}
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 2 || entries[0].Name() != "README.md" || entries[1].Name() != "snapshot.json" {
		t.Fatalf("export must contain only its two receipt files: %v %v", entries, err)
	}
	summary, err := os.ReadFile(filepath.Join(folder, "README.md"))
	if err != nil || !bytes.Contains(summary, []byte("No extra checks were configured")) || !bytes.Contains(summary, []byte("changes_requested")) {
		t.Fatalf("summary overstated task completion: %s %v", summary, err)
	}
	if again := knowledgeCLISave(t, run.RunID, target); again != folder {
		t.Fatalf("identical export changed destination: %s", again)
	}
	after, _ := knowledgeCLIRead(t, folder)
	if !bytes.Equal(before, after) {
		t.Fatal("identical export changed receipt bytes")
	}
	if got, err := os.ReadFile(index); err != nil || string(got) != "pre-existing staged work" {
		t.Fatalf("CLI export changed Git index: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); !os.IsNotExist(err) {
		t.Fatal("CLI export created a Git commit")
	}

	// A conflicting receipt and a destination blocked by an existing file both
	// fail visibly; failure must preserve the user's existing bytes.
	conflict := filepath.Join(folder, "snapshot.json")
	if err := os.WriteFile(conflict, []byte("existing receipt must survive"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "occupied-target")
	if err := os.WriteFile(blocked, []byte("occupied target must survive"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{target, blocked} {
		code, _ := knowledgeCLIOutput(t, func() int { return cmdKnowledgeSave([]string{run.RunID, "--target", destination, "--json"}, true) })
		if code == 0 {
			t.Fatalf("unwritable or conflicting destination reported success: %s", destination)
		}
	}
	for file, want := range map[string]string{conflict: "existing receipt must survive", blocked: "occupied target must survive"} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Fatalf("failed export changed %s: %q %v", file, got, err)
		}
	}
}

func TestKnowledgeSaveDoesNotApplySourceDecisionToContinuation(t *testing.T) {
	st := knowledgeCLIStore(t)
	source := fmt.Sprintf("knowledge-source-%d", time.Now().UnixNano())
	child := source + "-continued"
	for i, id := range []string{source, child} {
		parent := ""
		if i > 0 {
			parent = source
		}
		options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": map[string]string{"workflow": "node-workflow", "instruction": "Continue the same work"}, "continuation": map[string]string{"sourceRunId": parent}})
		if err := st.CreateRun(t.Context(), store.Run{RunID: id, DAG: "node-workflow", State: "interrupted", ParentRun: parent, Options: options, Doc: store.JSON(`{"sdk":{"phase":"interrupted","nodes":[],"verification":"not_requested"}}`)}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.DeleteRun(context.Background(), id) })
	}
	key := "j-" + strings.ReplaceAll(source, "-", "")
	oldID := store.JourneyAttemptOutcomeDecisionID(key, source)
	if _, err := st.CreateDecision(t.Context(), store.Decision{ID: oldID, Title: "Earlier attempt", Decision: "changes_requested", Tags: []string{store.OutcomeTag}, Refs: []string{"journey:" + key, "run:" + source}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), oldID) })
	first := knowledgeCLISave(t, child, t.TempDir())
	originalBytes, receipt := knowledgeCLIRead(t, first)
	if receipt.HumanOutcome != nil {
		t.Fatalf("continuation export inherited the source decision: %s", originalBytes)
	}
	currentID := store.JourneyAttemptOutcomeDecisionID(key, child)
	if _, err := st.CreateDecision(t.Context(), store.Decision{ID: currentID, Title: "Current attempt", Decision: "rejected", Tags: []string{store.OutcomeTag}, Refs: []string{"journey:" + key, "run:" + child}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteDecision(context.Background(), currentID) })
	current := knowledgeCLISave(t, child, t.TempDir())
	_, receipt = knowledgeCLIRead(t, current)
	if receipt.HumanOutcome == nil || receipt.HumanOutcome.ID != currentID || receipt.HumanOutcome.Decision != "rejected" {
		t.Fatalf("current attempt decision missing: %+v", receipt.HumanOutcome)
	}
	if after, _ := knowledgeCLIRead(t, first); !bytes.Equal(after, originalBytes) {
		t.Fatal("new human decision rewrote an old immutable export")
	}
}
