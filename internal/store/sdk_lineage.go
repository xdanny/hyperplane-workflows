package store

import "encoding/json"

// Continuation lineage is frozen in server-owned launch options. A parent link
// alone can also mean a batch child and does not authorize delivery grouping.
func sdkLineage(run Run) (source, root string) {
	var launch struct {
		Runtime      string `json:"runtime"`
		Continuation struct {
			Source string `json:"sourceRunId"`
			Root   string `json:"rootRunId"`
		} `json:"continuation"`
	}
	if json.Unmarshal(run.Options, &launch) != nil || launch.Runtime != "codex-sdk" || launch.Continuation.Source == "" || launch.Continuation.Source == run.RunID {
		return "", run.RunID
	}
	// DeleteRun clears relational parent pointers. The immutable launch root
	// retains delivery identity after that deletion without reading an ancestor.
	if run.ParentRun != launch.Continuation.Source && (run.ParentRun != "" || launch.Continuation.Root == "") {
		return "", run.RunID
	}
	root = launch.Continuation.Root
	if root == "" {
		root = launch.Continuation.Source
	}
	return launch.Continuation.Source, root
}

func SDKContinuationSource(run Run) string { source, _ := sdkLineage(run); return source }
func SDKDeliveryRoot(run Run) string       { _, root := sdkLineage(run); return root }
