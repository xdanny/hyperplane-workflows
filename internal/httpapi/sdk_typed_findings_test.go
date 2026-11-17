package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// A reviewer labels each finding and the SDK worker records the level it read
// off that label. The backend must carry those levels through untouched: it
// never assigns a level, and it never drops one, or a failed run's evidence
// goes back to reading confirmations as caveats (S-17).
func TestTypedReviewFindingsSurviveTheBackendProjection(t *testing.T) {
	doc := `{"sdk":{"phase":"failed","findings":["FAIL - The revenue claim has no source.","CONFIRMED - Dates match the brief."],
		"findingDetails":[{"level":"fail","text":"FAIL - The revenue claim has no source."},{"level":"confirmation","text":"CONFIRMED - Dates match the brief."}],
		"nodes":[{"id":"draft","title":"Draft","phase":"passed"},
		{"id":"check","title":"Review","phase":"failed","findings":["FAIL - The revenue claim has no source."],
		 "findingDetails":[{"level":"fail","text":"FAIL - The revenue claim has no source."}]}]}}`
	run := store.Run{State: "failed", Doc: json.RawMessage(doc)}

	got := string(sdkFailureEvidence(run))
	for _, want := range []string{`"level":"fail"`, `"level":"confirmation"`, `"id":"check"`} {
		if !strings.Contains(got, want) {
			t.Errorf("failure evidence lost %s: %s", want, got)
		}
	}
	// The plain strings every existing reader uses are unchanged.
	if !strings.Contains(got, "FAIL - The revenue claim has no source.") {
		t.Errorf("failure evidence lost the finding text: %s", got)
	}

	// The run view hands the committed checkpoint through as recorded, so the
	// levels reach a reader without the backend interpreting them.
	view := projectRun(run, "", 1)
	if !strings.Contains(string(view.Doc), `"findingDetails"`) {
		t.Errorf("run view dropped typed findings: %s", view.Doc)
	}

	// A record no typed review wrote carries no levels, and nothing invents one.
	old := store.Run{State: "failed", Doc: json.RawMessage(`{"sdk":{"phase":"failed","findings":["Evidence is limited"],"nodes":[{"id":"check","title":"Review","phase":"failed","findings":["Evidence is limited"]}]}}`)}
	if evidence := string(sdkFailureEvidence(old)); strings.Contains(evidence, "findingDetails") {
		t.Errorf("invented typed findings for an older run: %s", evidence)
	}
}
