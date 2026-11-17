package httpapi

import (
	"encoding/json"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// reviewFinding is one independent-review finding with the level the SDK worker
// derived from the reviewer's own label. Carried through unchanged: the backend
// never assigns or re-reads a level.
type reviewFinding struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// sdkFailureEvidence reports checkpoint findings without inferring graph gates
// or claiming that file production independently verifies correctness.
func sdkFailureEvidence(run store.Run) json.RawMessage {
	if run.State != "failed" && run.State != "interrupted" {
		return nil
	}
	var doc struct {
		SDK struct {
			Phase string `json:"phase"`
			Error string `json:"error,omitempty"`
			// Findings keeps the plain strings every existing reader uses.
			// FindingDetails is the SAME list typed by the reviewer's own label
			// (fail, caveat, confirmation) and must survive this projection, or a
			// failed run's evidence goes back to reading three confirmations as
			// three caveats. Absent on records no typed review wrote.
			Findings       []string        `json:"findings,omitempty"`
			FindingDetails []reviewFinding `json:"findingDetails,omitempty"`
			Nodes          []struct {
				ID             string          `json:"id"`
				Title          string          `json:"title"`
				Phase          string          `json:"phase"`
				Verification   string          `json:"verification,omitempty"`
				Findings       []string        `json:"findings,omitempty"`
				FindingDetails []reviewFinding `json:"findingDetails,omitempty"`
			} `json:"nodes,omitempty"`
		} `json:"sdk"`
	}
	if json.Unmarshal(run.Doc, &doc) != nil || doc.SDK.Phase == "" {
		return nil
	}
	failed := doc.SDK.Nodes[:0]
	for _, node := range doc.SDK.Nodes {
		if node.Phase == "failed" || node.Phase == "rejected" || node.Phase == "interrupted" {
			failed = append(failed, node)
		}
	}
	doc.SDK.Nodes = failed
	raw, _ := json.Marshal(doc.SDK)
	return raw
}
