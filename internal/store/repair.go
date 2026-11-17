package store

import (
	"encoding/json"
)

// Legacy repair markers remain readable by journey and knowledge views.
// SDK correction attempts are recorded in their checkpoint instead.
const (
	RepairedByKey       = "repaired_by"
	RepairOfKey         = "repair_of"
	RepairTriggerKey    = "repair_trigger"
	RepairPointKey      = "repair_point"
	RepairTriggerAuto   = "auto"
	RepairTriggerManual = "manual"
)

// RunDocString reads a top-level string field out of a run's doc, returning ""
// when the run, the field, or a string value is absent.
func RunDocString(r Run, key string) string {
	if len(r.Doc) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(r.Doc, &m); err != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}
