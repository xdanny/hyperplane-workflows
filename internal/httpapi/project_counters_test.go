package httpapi_test

import (
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// Saving a workflow into a project and then reading "WORKFLOWS 0" is the
// project surface calling the person wrong. A saved workflow counts whether or
// not it has run, and an ad-hoc run group counts once.
func TestProjectCountsSavedWorkflowsAndAdHocGroups(t *testing.T) {
	srv, ts := newTestServer(t, nil)
	c := signOffClient{t: t, url: ts.URL}
	st := srv.Store()
	project := "counter-project-" + time.Now().UTC().Format("150405.000000")
	target := t.TempDir()

	c.call("POST", "/api/projects", map[string]any{"id": project, "name": "Counters"}, 201, nil)
	t.Cleanup(func() { _, _ = st.Pool.Exec(t.Context(), `DELETE FROM projects WHERE id=$1`, project) })

	countFor := func() (int, int) {
		t.Helper()
		var projects []store.ProjectCount
		c.call("GET", "/api/projects?includeTest=true", nil, 200, &projects)
		for _, p := range projects {
			if p.Project == project {
				return p.WorkflowCount, p.Runs
			}
		}
		t.Fatalf("project %q missing from the list", project)
		return 0, 0
	}
	if workflows, runs := countFor(); workflows != 0 || runs != 0 {
		t.Fatalf("empty project: workflows=%d runs=%d", workflows, runs)
	}

	var saved store.SDKWorkflow
	c.call("POST", "/api/workflows", store.SDKWorkflow{Name: "Counted workflow", Kind: "task", Project: project,
		Instruction: "Write the answer", TargetRoot: target}, 201, &saved)
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(t.Context(), `DELETE FROM sdk_workflows WHERE id=$1`, saved.ID)
	})

	// A saved workflow that has never run is still a workflow of this project.
	if workflows, runs := countFor(); workflows != 1 || runs != 0 {
		t.Fatalf("after saving: workflows=%d runs=%d", workflows, runs)
	}
	var detail store.ProjectDetail
	c.call("GET", "/api/projects/"+project, nil, 200, &detail)
	if len(detail.Workflows) != 1 {
		t.Fatalf("detail workflows: %+v", detail.Workflows)
	}
	row := detail.Workflows[0]
	if row.Runs != 0 || row.SourceWorkflowID != saved.ID || row.Name != "Counted workflow" {
		t.Fatalf("saved-but-unrun row: %+v", row)
	}

	// An ad-hoc run of different settings is a second group; a run of the saved
	// workflow is not a third.
	adHoc := seedAdHocRun(t, st, project, target, "Something else entirely", "passed")
	if workflows, runs := countFor(); workflows != 2 || runs != 1 {
		t.Fatalf("after an ad-hoc run: workflows=%d runs=%d", workflows, runs)
	}
	if err := st.AttachRunToWorkflow(t.Context(), store.SDKWorkflow{}, adHoc); err == nil {
		t.Fatal("attaching to an empty workflow must be refused")
	}
}
