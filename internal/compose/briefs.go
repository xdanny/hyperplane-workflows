package compose

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// StarterBrief is a canonical compose brief preset. It surfaces in the
// UI/CLI as a one-click starting point for a common workflow.
type StarterBrief struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Brief    string `json:"brief"`
	// Recipe names the seeded recipe (workflow template) that asks for the same
	// facts as separate labelled fields. When it is set, the UI's starter chip
	// opens THAT form instead of pasting prose — one workflow, one name, one
	// door (Q-24, Q-25). Brief stays as the fallback for an install where the
	// recipe is not seeded, and it is deliberately short and labelled.
	Recipe string `json:"recipe,omitempty"`
}

//go:embed briefs.json
var starterBriefsJSON []byte

// StarterBriefs returns the built-in compose brief presets.
func StarterBriefs() ([]StarterBrief, error) {
	var briefs []StarterBrief
	if err := json.Unmarshal(starterBriefsJSON, &briefs); err != nil {
		return nil, fmt.Errorf("compose: parse built-in briefs: %w", err)
	}
	return briefs, nil
}
