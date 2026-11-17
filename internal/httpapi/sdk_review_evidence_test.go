package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestSDKReviewEvidenceUsesCheckpointFailures(t *testing.T) {
	run := store.Run{State: "failed", Doc: json.RawMessage(`{"sdk":{"phase":"failed","findings":["Check program exited 1"],"nodes":[{"id":"write","title":"Create files","phase":"passed","verification":"not_requested"},{"id":"check","title":"Check files","phase":"failed","verification":"failed"},{"id":"later","title":"Later","phase":"pending"}]}}`)}
	got := string(sdkFailureEvidence(run))
	for _, want := range []string{"Check program exited 1", `"id":"check"`, `"verification":"failed"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, absent := range []string{`"id":"write"`, `"id":"later"`, "selfHeal", "gate"} {
		if strings.Contains(got, absent) {
			t.Errorf("unexpected %s in %s", absent, got)
		}
	}
	run.State = "passed"
	if got := sdkFailureEvidence(run); got != nil {
		t.Fatalf("successful run claimed failures: %s", got)
	}
	run.State = "interrupted"
	run.Doc = json.RawMessage(`{"sdk":{"phase":"interrupted","error":"Cancelled by owner"}}`)
	if got := string(sdkFailureEvidence(run)); !strings.Contains(got, "Cancelled by owner") {
		t.Fatalf("lost interruption: %s", got)
	}
}
