package contract

import "encoding/json"

// LegacyView projects an envelope into the raw shape expected by readers that
// predate the internode contract.
func LegacyView(envelope Envelope) json.RawMessage {
	if len(envelope.LegacyOutputs) > 0 {
		return append(json.RawMessage(nil), envelope.LegacyOutputs...)
	}
	out := map[string]json.RawMessage{}
	for name, value := range envelope.Outputs {
		if name == "legacy" && value.Artifact == nil {
			var fields map[string]json.RawMessage
			if json.Unmarshal(value.Inline, &fields) == nil {
				for key, raw := range fields {
					out[key] = raw
				}
				continue
			}
		}
		b, err := json.Marshal(value)
		if err == nil {
			out[name] = b
		}
	}
	if envelope.Verdict != nil {
		out["pass"], _ = json.Marshal(envelope.Verdict.Pass)
		out["reasons"], _ = json.Marshal(envelope.Verdict.Reasons)
		if envelope.Verdict.Evidence != nil {
			out["evidence"], _ = json.Marshal(envelope.Verdict.Evidence)
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// DecodeEnvelope distinguishes contract envelopes from legacy raw node output.
func DecodeEnvelope(raw json.RawMessage) (Envelope, bool) {
	var probe struct {
		OK         *bool           `json:"ok"`
		Outputs    json.RawMessage `json:"outputs"`
		Provenance json.RawMessage `json:"provenance"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.OK == nil || probe.Outputs == nil || probe.Provenance == nil {
		return Envelope{}, false
	}
	var env Envelope
	if json.Unmarshal(raw, &env) != nil {
		return Envelope{}, false
	}
	return env, true
}

// LegacyViewJSON is the tolerant form used at compatibility read sites.
func LegacyViewJSON(raw json.RawMessage) json.RawMessage {
	if env, ok := DecodeEnvelope(raw); ok {
		return LegacyView(env)
	}
	return raw
}
