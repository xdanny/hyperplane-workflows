// Package contract defines SDK artifact envelopes and local JSON Schema validation.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ArtifactRef identifies a file relative to its run folder.
type ArtifactRef struct {
	Artifact string `json:"artifact"`
	Media    string `json:"media"`
	SHA256   string `json:"sha256,omitempty"`
	Bytes    int64  `json:"bytes"`
}

// Value is the contract's tagged union. Inline values marshal as their JSON
// value; artifact values marshal as an ArtifactRef object.
type Value struct {
	Inline   json.RawMessage `json:"-"`
	Artifact *ArtifactRef    `json:"-"`
}

func InlineValue(raw json.RawMessage) Value {
	return Value{Inline: append(json.RawMessage(nil), raw...)}
}

func ArtifactValue(ref ArtifactRef) Value { return Value{Artifact: &ref} }

func (v Value) MarshalJSON() ([]byte, error) {
	if v.Artifact != nil {
		return json.Marshal(v.Artifact)
	}
	if len(bytes.TrimSpace(v.Inline)) == 0 {
		return []byte("null"), nil
	}
	if !json.Valid(v.Inline) {
		return nil, fmt.Errorf("contract value contains invalid inline JSON")
	}
	return v.Inline, nil
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var probe struct {
		Artifact *string `json:"artifact"`
		Media    string  `json:"media"`
		SHA256   string  `json:"sha256"`
		Bytes    int64   `json:"bytes"`
	}
	if len(data) > 0 && data[0] == '{' && json.Unmarshal(data, &probe) == nil && probe.Artifact != nil {
		v.Artifact = &ArtifactRef{Artifact: *probe.Artifact, Media: probe.Media, SHA256: probe.SHA256, Bytes: probe.Bytes}
		v.Inline = nil
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("contract value is not valid JSON")
	}
	v.Inline = append(v.Inline[:0], data...)
	v.Artifact = nil
	return nil
}

type Verdict struct {
	Pass     bool         `json:"pass"`
	Reasons  string       `json:"reasons"`
	Evidence *ArtifactRef `json:"evidence,omitempty"`
}

type BoundInput struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

type Provenance struct {
	Run       string                `json:"run"`
	Node      string                `json:"node"`
	Engine    string                `json:"engine"`
	Model     string                `json:"model"`
	StartedAt string                `json:"startedAt"`
	EndedAt   string                `json:"endedAt"`
	Inputs    map[string]BoundInput `json:"inputs,omitempty"`
}

type Envelope struct {
	OK         bool             `json:"ok"`
	Outputs    map[string]Value `json:"outputs"`
	Verdict    *Verdict         `json:"verdict,omitempty"`
	Provenance Provenance       `json:"provenance"`
	Notes      string           `json:"notes,omitempty"`
	// LegacyOutputs preserves the pre-contract executor shape for one release.
	// New references never resolve through it.
	LegacyOutputs json.RawMessage `json:"legacyOutputs,omitempty"`
}
