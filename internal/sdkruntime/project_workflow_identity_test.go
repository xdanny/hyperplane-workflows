package sdkruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestProjectWorkflowAdmissionDoesNotCreateRun(t *testing.T) {
	st := runtimeStore(t)
	for _, kind := range []string{"nodes", "task", "app-ideator"} {
		for _, invalid := range []string{"missing-source", "long-name", "malformed-source"} {
			t.Run(kind+"/"+invalid, func(t *testing.T) {
				runs := filepath.Join(t.TempDir(), "not-created")
				started := make(chan error, 1)
				opts := Options{RunID: NewRunID("rejected-source"), RunsDir: runs, TargetRoot: t.TempDir(), Started: started}
				switch invalid {
				case "missing-source":
					opts.SourceWorkflowID = "missing-source"
				case "long-name":
					opts.WorkflowName = strings.Repeat("é", 81)
				case "malformed-source":
					opts.SourceWorkflowID = "invalid\x00source"
				}
				var err error
				switch kind {
				case "nodes":
					_, err = RunNodeWorkflow(t.Context(), st, opts, "Write notes", []sdkworkflow.Node{{ID: "write", Title: "Write", Instruction: "Write notes"}})
				case "task":
					_, err = RunCheckedTask(t.Context(), st, opts, "Write notes", nil)
				case "app-ideator":
					_, err = RunAppIdeator(t.Context(), st, opts)
				}
				if !errors.Is(err, store.ErrInvalidWorkflow) {
					t.Fatalf("admission error: %v", err)
				}
				if ack := <-started; !errors.Is(ack, store.ErrInvalidWorkflow) {
					t.Fatalf("startup acknowledgement: %v", ack)
				}
				if _, err := os.Stat(runs); !os.IsNotExist(err) {
					t.Fatalf("invalid source created workspace: %v", err)
				}
				if _, err := st.GetRun(t.Context(), opts.RunID); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("invalid source persisted run: %v", err)
				}
			})
		}
	}
}

func TestContinuationKeepsProjectWorkflowIdentity(t *testing.T) {
	st := runtimeStore(t)
	for _, mode := range []string{"saved", "older-sdk"} {
		t.Run(mode, func(t *testing.T) {
			saved, err := st.SaveSDKWorkflow(t.Context(), store.SDKWorkflow{Name: "Original source", Kind: "app-ideator", Instruction: "Stored definition"})
			if err != nil {
				t.Fatal(err)
			}
			project := NewRunID("identity-project")
			opts, _, source := interruptedWorkflow(t, st, false, func(opts *Options) {
				opts.Project = project
				if mode == "saved" {
					opts.SourceWorkflowID, opts.WorkflowName = saved.ID, "Unsaved title"
				}
			})
			if mode == "older-sdk" {
				// Simulate a previously admitted SDK run with no organizational metadata.
				if _, err := st.Pool.Exec(t.Context(), `UPDATE runs SET options=options-'projectWorkflow' WHERE run_id=$1`, source.RunID); err != nil {
					t.Fatal(err)
				}
				source, err = st.GetRun(t.Context(), source.RunID)
				if err != nil {
					t.Fatal(err)
				}
			}
			frozen, err := st.ProjectWorkflowIdentity(t.Context(), source.Options, source.DAG, source.Goal, source.Doc)
			if err != nil {
				t.Fatal(err)
			}
			var expected struct{ Key, Name string }
			if err := json.Unmarshal(frozen, &expected); err != nil {
				t.Fatal(err)
			}
			saved.Name = "Renamed after launch"
			saved, err = st.SaveSDKWorkflow(t.Context(), saved)
			if err != nil {
				t.Fatal(err)
			}
			before, err := st.GetProjectDetail(t.Context(), project)
			if err != nil || len(before.Workflows) != 1 || before.Workflows[0].Name != expected.Name {
				t.Fatalf("rename changed run group: %+v %v", before, err)
			}
			if err := st.DeleteSDKWorkflow(t.Context(), saved.ID, saved.Revision); err != nil {
				t.Fatal(err)
			}
			review, err := ReviewContinuation(t.Context(), st, opts.RunsDir, source.RunID)
			if err != nil || !review.Eligible {
				t.Fatalf("review: %+v %v", review, err)
			}
			t.Setenv("SDK_FIXTURE_MODE", "ordinary")
			t.Setenv("SDK_FIXTURE_CONTINUATION", "1")
			child := Options{RunID: NewRunID("continued-identity"), RunsDir: opts.RunsDir, SourceWorkflowID: "missing-client-override", WorkflowName: "must not override snapshot"}
			result, err := RunContinuation(t.Context(), st, child, source.RunID, review.ReviewToken)
			if err != nil || result.State != "passed" {
				t.Fatalf("continue: %+v %v", result, err)
			}
			continued, err := st.GetRun(t.Context(), result.RunID)
			if err != nil {
				t.Fatal(err)
			}
			var launch struct{ ProjectWorkflow json.RawMessage }
			if err := json.Unmarshal(continued.Options, &launch); err != nil {
				t.Fatal(err)
			}
			var actual struct{ Key, Name string }
			if err := json.Unmarshal(launch.ProjectWorkflow, &actual); err != nil || actual != expected {
				t.Fatalf("continuation identity changed: %s %v", continued.Options, err)
			}
			detail, err := st.GetProjectDetail(t.Context(), project)
			if err != nil || detail.WorkflowCount != 1 || len(detail.Workflows) != 1 || detail.Workflows[0].Runs != 2 || detail.Workflows[0].LastRunID != result.RunID {
				t.Fatalf("continued grouping: %+v %v", detail, err)
			}
			if err := st.DeleteRun(t.Context(), source.RunID); err != nil {
				t.Fatal(err)
			}
			after, err := st.GetProjectDetail(t.Context(), project)
			if err != nil || after.WorkflowCount != 1 || len(after.Workflows) != 1 || after.Workflows[0].Key != expected.Key || after.Workflows[0].Name != expected.Name || after.Workflows[0].Runs != 1 || after.Workflows[0].LastRunID != result.RunID {
				t.Fatalf("source deletion regrouped continuation: %+v %v", after, err)
			}
		})
	}
}
