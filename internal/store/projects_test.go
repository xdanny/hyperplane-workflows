package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// findProject picks one project out of a ListProjects result.
func findProject(list []ProjectCount, id string) (ProjectCount, bool) {
	for _, pc := range list {
		if pc.Project == id {
			return pc, true
		}
	}
	return ProjectCount{}, false
}

// TestProjectCRUD covers create (insert-only), get, and partial update.
func TestProjectCRUD(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "crud-proj-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id) })

	created, err := st.CreateProject(ctx, Project{ID: id, Summary: "first"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Name and status default rather than landing empty.
	if created.Name == "" || created.Status != "active" {
		t.Fatalf("expected defaulted name+status, got %+v", created)
	}
	if created.Provenance != ProjectProvenanceUser {
		t.Fatalf("expected provenance %q, got %q", ProjectProvenanceUser, created.Provenance)
	}

	// Insert-only: a second create must not overwrite the first.
	if _, err := st.CreateProject(ctx, Project{ID: id, Summary: "clobbered"}); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists on duplicate create, got %v", err)
	}

	got, err := st.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if got.Summary != "first" {
		t.Fatalf("duplicate create overwrote the summary: %q", got.Summary)
	}

	// PATCH semantics: an absent field is left alone.
	newSummary := "edited"
	updated, err := st.UpdateProject(ctx, id, ProjectPatch{Summary: &newSummary})
	if err != nil {
		t.Fatalf("update project: %v", err)
	}
	if updated.Summary != newSummary {
		t.Fatalf("summary not updated: %+v", updated)
	}
	if updated.Name != created.Name {
		t.Fatalf("absent name field was not left alone: %q -> %q", created.Name, updated.Name)
	}

	if _, err := st.GetProject(ctx, "no-such-project-"+uniq()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	missing := "x"
	if _, err := st.UpdateProject(ctx, "no-such-project-"+uniq(), ProjectPatch{Name: &missing}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on patching a missing project, got %v", err)
	}
}

// TestEnsureProjectAutoRegister asserts the store-level hook: creating a run, a
// lane or a decision with a project tag registers that project, and the
// unscoped tag ("") never becomes a row.
func TestEnsureProjectAutoRegister(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()
	runProj := "auto-run-" + suffix
	laneProj := "auto-lane-" + suffix
	decProj := "auto-dec-" + suffix
	runID := "test-run-" + suffix
	laneID := "test-lane-" + suffix
	decID := "test-dec-" + suffix

	t.Cleanup(func() {
		_ = st.DeleteRun(ctx, runID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM lanes WHERE id=$1`, laneID)
		_ = st.DeleteDecision(ctx, decID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id = ANY($1)`,
			[]string{runProj, laneProj, decProj})
	})

	if err := st.CreateRun(ctx, Run{RunID: runID, DAG: "hello", State: "running", Project: runProj}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.CreateLane(ctx, &Lane{ID: laneID, Name: "lane", Status: "active", Project: laneProj}); err != nil {
		t.Fatalf("create lane: %v", err)
	}
	if _, err := st.UpsertDecision(ctx, Decision{ID: decID, Title: "d", Project: decProj}); err != nil {
		t.Fatalf("upsert decision: %v", err)
	}

	for _, id := range []string{runProj, laneProj, decProj} {
		p, err := st.GetProject(ctx, id)
		if err != nil {
			t.Fatalf("project %q was not auto-registered: %v", id, err)
		}
		if p.Name == "" {
			t.Fatalf("auto-registered project %q has no display name", id)
		}
	}

	// "auto-run-<stamp>" → "Auto Run <stamp>": the Go hook and the SQL backfill
	// derive the same display name.
	if got := defaultProjectName("data-engineering"); got != "Data Engineering" {
		t.Fatalf("defaultProjectName: got %q, want %q", got, "Data Engineering")
	}

	// The unscoped tag is not a project.
	if err := st.EnsureProject(ctx, ""); err != nil {
		t.Fatalf("EnsureProject(\"\"): %v", err)
	}
	if _, err := st.GetProject(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the empty tag must never become a project row, got %v", err)
	}

	// The hook is idempotent — re-registering does not fail or duplicate.
	if err := st.EnsureProject(ctx, runProj); err != nil {
		t.Fatalf("re-ensure: %v", err)
	}
}

// TestListProjectsCountsAndZeroItemRows asserts the list keeps its original
// {project,count} meaning, breaks the count down per kind, and includes a
// registered project that has nothing tagged with it yet.
func TestListProjectsCountsAndZeroItemRows(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()
	proj := "counted-" + suffix
	empty := "empty-" + suffix
	runID := "test-run-" + suffix
	laneID := "test-lane-" + suffix
	decID := "test-dec-" + suffix

	t.Cleanup(func() {
		_ = st.DeleteRun(ctx, runID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM lanes WHERE id=$1`, laneID)
		_ = st.DeleteDecision(ctx, decID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id = ANY($1)`, []string{proj, empty})
	})

	if err := st.CreateRun(ctx, Run{RunID: runID, DAG: "hello", State: "passed", Project: proj}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.CreateLane(ctx, &Lane{ID: laneID, Name: "lane", Status: "active", Project: proj}); err != nil {
		t.Fatalf("create lane: %v", err)
	}
	if _, err := st.UpsertDecision(ctx, Decision{ID: decID, Title: "d", Project: proj}); err != nil {
		t.Fatalf("upsert decision: %v", err)
	}
	if _, err := st.CreateProject(ctx, Project{ID: empty, Name: "Empty"}); err != nil {
		t.Fatalf("create empty project: %v", err)
	}

	list, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}

	pc, ok := findProject(list, proj)
	if !ok {
		t.Fatalf("project %q missing from list", proj)
	}
	if pc.Runs != 1 || pc.Lanes != 1 || pc.Decisions != 1 {
		t.Fatalf("per-kind counts wrong: %+v", pc)
	}
	if pc.Count != 3 {
		t.Fatalf("combined count: got %d, want 3 (%+v)", pc.Count, pc)
	}
	if pc.WorkflowCount != 1 {
		t.Fatalf("workflow count: got %d, want 1", pc.WorkflowCount)
	}
	if !pc.Registered || pc.Name == "" {
		t.Fatalf("expected the auto-registered row's metadata on the list entry: %+v", pc)
	}
	if pc.LastActivity == nil {
		t.Fatalf("expected last activity on a project with items")
	}

	// The point of the table: a project with zero items is still a project.
	ec, ok := findProject(list, empty)
	if !ok {
		t.Fatalf("registered project with no items was dropped from the list")
	}
	if ec.Count != 0 || !ec.Registered || ec.Name != "Empty" {
		t.Fatalf("unexpected zero-item entry: %+v", ec)
	}
	if ec.LastActivity == nil {
		t.Fatalf("a zero-item project should fall back to its own updated_at")
	}
}

// TestListProjectsHidesTestProvenance asserts the includeTest switch, which
// only matters on a database shared with the data-engineering line's fixtures.
func TestListProjectsHidesTestProvenance(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "fixture-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id) })
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO projects (id, name, provenance) VALUES ($1,$2,'test')`, id, "Fixture"); err != nil {
		t.Fatalf("insert test-provenance project: %v", err)
	}

	hidden, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if _, ok := findProject(hidden, id); ok {
		t.Fatalf("a provenance='test' project leaked into the default list")
	}

	shown, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list projects (includeTest): %v", err)
	}
	if _, ok := findProject(shown, id); !ok {
		t.Fatalf("includeTest=true did not surface the fixture project")
	}
}

// TestProjectWorkflowsDerivedFromRuns asserts "workflows of a project" is
// derived from the runs that actually carry the tag, grouped by DAG and by the
// recipe a run was instantiated from.
func TestProjectWorkflowsDerivedFromRuns(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	suffix := uniq()
	proj := "wf-" + suffix
	runs := []string{"test-run-a-" + suffix, "test-run-b-" + suffix, "test-run-c-" + suffix}
	t.Cleanup(func() {
		for _, id := range runs {
			_ = st.DeleteRun(ctx, id)
		}
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, proj)
	})

	base := time.Now().Add(-time.Hour)
	for i, id := range runs {
		dag := "hello"
		doc := JSON(`{}`)
		if i == 2 {
			dag = "research"
			doc = JSON(`{"recipe":"market-research"}`)
		}
		if err := st.CreateRun(ctx, Run{
			RunID: id, DAG: dag, State: "passed", Project: proj,
			StartedAt: base.Add(time.Duration(i) * time.Minute), Doc: doc,
		}); err != nil {
			t.Fatalf("create run %s: %v", id, err)
		}
	}

	wfs, err := st.ListProjectWorkflows(ctx, proj)
	if err != nil {
		t.Fatalf("list project workflows: %v", err)
	}
	if len(wfs) != 2 {
		t.Fatalf("expected 2 workflows (hello + the recipe run), got %d: %+v", len(wfs), wfs)
	}
	byDAG := map[string]ProjectWorkflow{}
	for _, wf := range wfs {
		byDAG[wf.DAG] = wf
	}
	if byDAG["hello"].Runs != 2 {
		t.Fatalf("expected 2 runs of hello, got %d", byDAG["hello"].Runs)
	}
	if byDAG["research"].Recipe != "market-research" {
		t.Fatalf("recipe not derived from the run doc: %+v", byDAG["research"])
	}
	if byDAG["hello"].LastRunID == "" || byDAG["hello"].LastState != "passed" {
		t.Fatalf("expected the newest run stamped on the workflow: %+v", byDAG["hello"])
	}

	detail, err := st.GetProjectDetail(ctx, proj)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}
	if detail.Project != proj || len(detail.Workflows) != 2 || detail.Runs != 3 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	if _, err := st.GetProjectDetail(ctx, "no-such-project-"+uniq()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown project, got %v", err)
	}
}

// TestProjectsMigrationIsIdempotent re-applies the projects migration against a
// database that already has it. It is the converge-or-no-op guarantee the
// migration is written for: the same file also has to be inert on a database
// carrying the data-engineering line's own projects table.
func TestProjectsMigrationIsIdempotent(t *testing.T) {
	st := testStore(t) // testStore already ran Migrate once
	defer st.Close()
	ctx := context.Background()

	sqlBytes, err := migrationsFS.ReadFile("migrations/0022_projects.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	before := projectsShape(t, st)
	for i := 0; i < 2; i++ {
		if _, err := st.Pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("re-apply #%d failed — the migration is not idempotent: %v", i+1, err)
		}
	}
	if after := projectsShape(t, st); after != before {
		t.Fatalf("re-applying the migration changed the table shape:\nbefore: %s\nafter:  %s", before, after)
	}
}

// projectsShape renders the projects table's columns/types/defaults as one
// comparable string.
func projectsShape(t *testing.T, st *Store) string {
	t.Helper()
	rows, err := st.Pool.Query(context.Background(), `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_name = 'projects'
		ORDER BY column_name`)
	if err != nil {
		t.Fatalf("read projects shape: %v", err)
	}
	defer rows.Close()
	shape := ""
	for rows.Next() {
		var name, typ, nullable, def string
		if err := rows.Scan(&name, &typ, &nullable, &def); err != nil {
			t.Fatalf("scan projects shape: %v", err)
		}
		shape += name + " " + typ + " null=" + nullable + " default=" + def + "; "
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("projects shape rows: %v", err)
	}
	if shape == "" {
		t.Fatal("projects table has no columns — did the migration apply?")
	}
	return shape
}
