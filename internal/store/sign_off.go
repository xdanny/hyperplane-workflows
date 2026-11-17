package store

import "encoding/json"

// A run carries the sign-off policy it was LAUNCHED under, frozen in the
// server-owned launch options beside its organizational identity. Reading the
// saved workflow instead would let an edit made today restate the terms of a
// run someone finished last week — the same reason the definition itself is
// frozen. A run with no recorded policy (every run that predates the setting,
// and every ad-hoc launch) is "required", which is what those runs did.

// RunSignOff returns the sign-off policy recorded on a run.
func RunSignOff(run Run) string {
	var launch struct {
		ProjectWorkflow struct {
			SignOff string `json:"signOff"`
		} `json:"projectWorkflow"`
	}
	if len(run.Options) == 0 || json.Unmarshal(run.Options, &launch) != nil {
		return SignOffRequired
	}
	if !ValidSignOff(launch.ProjectWorkflow.SignOff) {
		return SignOffRequired
	}
	return launch.ProjectWorkflow.SignOff
}

// RunSlotLabels returns the labels the frozen request recorded for this run's
// input values, or nil when it recorded none.
func RunSlotLabels(run Run) map[string]string {
	var launch struct {
		Request struct {
			SlotLabels map[string]string `json:"slotLabels"`
		} `json:"request"`
	}
	if len(run.Options) == 0 || json.Unmarshal(run.Options, &launch) != nil {
		return nil
	}
	if len(launch.Request.SlotLabels) == 0 {
		return nil
	}
	return launch.Request.SlotLabels
}
