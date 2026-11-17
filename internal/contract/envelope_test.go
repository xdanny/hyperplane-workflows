package contract_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/contract"
)

func TestSDKArtifactEnvelopeRoundTrip(t *testing.T) {
	original := contract.Envelope{OK: true, Outputs: map[string]contract.Value{
		"report": contract.ArtifactValue(contract.ArtifactRef{Artifact: "nodes/delivery/OUTPUTS/report.md", Media: "text/markdown", Bytes: 12, SHA256: "recorded-hash"}),
		"count":  contract.InlineValue(json.RawMessage(`3`)),
	}, Verdict: &contract.Verdict{Pass: true, Reasons: "Requested files produced"}, Provenance: contract.Provenance{Run: "sdk-run", Node: "delivery", Engine: "codex-sdk"}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := contract.DecodeEnvelope(raw)
	if !ok || !decoded.OK || decoded.Outputs["report"].Artifact == nil {
		t.Fatalf("lost artifact envelope: %s", raw)
	}
	if *decoded.Outputs["report"].Artifact != *original.Outputs["report"].Artifact {
		t.Fatalf("artifact changed: %+v", decoded.Outputs["report"])
	}
	projected := contract.LegacyViewJSON(raw)
	for _, field := range []string{`"count":3`, `"pass":true`, `"reasons":"Requested files produced"`, `"artifact":"nodes/delivery/OUTPUTS/report.md"`} {
		if !strings.Contains(string(projected), field) {
			t.Fatalf("reader projection lost %s: %s", field, projected)
		}
	}
	// Existing provenance remains readable without graph binding or resolution.
	var provenance contract.Provenance
	if err := json.Unmarshal([]byte(`{"inputs":{"source":{"ref":"@source.result","sha256":"abc"}}}`), &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Inputs["source"].Ref != "@source.result" {
		t.Fatalf("lost stored provenance: %+v", provenance)
	}
}
