package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xdanny/hyperplane-workflows/internal/dbtest"
)

// replaceDBName swaps the database out of a DSN, so a scratch database can be
// reached with the same credentials as the configured one.
func replaceDBName(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + name
	return u.String()
}

// TestProjectDeclarationsMigrationOnAFreshDatabase applies EVERY migration, in
// order, into an empty database — the "clone and run" path — and then checks
// the two columns 0026 adds are actually there with the defaults the Go code
// assumes. It then reproduces the state every EXISTING install is in (0025
// applied, 0026 not) and applies 0026 alone, because a migration that only
// works on the author's database is one that fails on the first fresh install
// and a migration that only works on a fresh install is one that fails on every
// existing one.
func TestProjectDeclarationsMigrationOnAFreshDatabase(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping migration test")
	}
	if err := dbtest.GuardTestDSN(dsn); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A whole scratch DATABASE, not a schema: migration 0001 creates the pg_trgm
	// extension and indexes against gin_trgm_ops, which a schema-scoped
	// search_path cannot resolve. A fresh database is also what "clone and run"
	// actually means.
	dbName := fmt.Sprintf("hyperplane_test_store_migration_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("no postgres reachable (%v)", err)
	}
	defer admin.Close(context.Background())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Skipf("cannot create a scratch database here (%v) — skipping the fresh-install migration test", err)
	}
	t.Cleanup(func() {
		c, cerr := pgx.Connect(context.Background(), dsn)
		if cerr != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
	})

	scratchDSN := replaceDBName(dsn, dbName)
	if err := dbtest.GuardTestDSN(scratchDSN); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("connect to the scratch database: %v", err)
	}
	defer conn.Close(context.Background())
	const schema = "public"

	names := migrationNames(t)
	for _, name := range names {
		sqlBytes, rerr := migrationsFS.ReadFile("migrations/" + name)
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("migration %s failed on a fresh database: %v", name, err)
		}
	}

	assertDeclarationColumns(ctx, t, conn, schema)

	// ── the other half: a database that already carries 0025 but not 0026 ──
	//
	// Simulated by removing exactly what 0026 adds and re-applying it, which is
	// the state every existing install is in when this ships.
	if _, err := conn.Exec(ctx,
		"ALTER TABLE projects DROP COLUMN narrative, DROP COLUMN declarations"); err != nil {
		t.Fatalf("simulate a pre-0026 database: %v", err)
	}
	// A project that had a delivery accepted under the old build: the machine
	// narrative is sitting in the human's field, which is the bug.
	if _, err := conn.Exec(ctx,
		`INSERT INTO projects (id, name, summary) VALUES ('overwritten', 'Overwritten', $1)`,
		"This project built a verified daily revenue mart and audited six months of trips."); err != nil {
		t.Fatalf("seed the overwritten project: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO briefs (id, subject_kind, subject_id, project, body, grounded, engine, inputs_digest)
		 VALUES ('b-1', 'project', 'overwritten', 'overwritten', '{}', true, 'fallback', 'x')`); err != nil {
		t.Fatalf("seed the project brief: %v", err)
	}
	// A project nobody ever accepted a delivery under: a hand-typed description
	// that the migration must not touch.
	if _, err := conn.Exec(ctx,
		`INSERT INTO projects (id, name, summary) VALUES ('untouched', 'Untouched', 'What I want out of this.')`); err != nil {
		t.Fatalf("seed the untouched project: %v", err)
	}

	sqlBytes, err := migrationsFS.ReadFile("migrations/0026_project_declarations.sql")
	if err != nil {
		t.Fatalf("read 0026: %v", err)
	}
	if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("0026 failed on a database that already has 0025: %v", err)
	}
	assertDeclarationColumns(ctx, t, conn, schema)

	var summary, narrative string
	if err := conn.QueryRow(ctx,
		`SELECT summary, narrative FROM projects WHERE id='overwritten'`).Scan(&summary, &narrative); err != nil {
		t.Fatalf("read back the overwritten project: %v", err)
	}
	if narrative == "" {
		t.Error("the machine narrative was not moved out of the human's field")
	}
	if summary != "" {
		t.Errorf("summary still holds the machine narrative: %q", summary)
	}
	if err := conn.QueryRow(ctx,
		`SELECT summary, narrative FROM projects WHERE id='untouched'`).Scan(&summary, &narrative); err != nil {
		t.Fatalf("read back the untouched project: %v", err)
	}
	if summary != "What I want out of this." {
		t.Errorf("a hand-typed description on a project with no accepted delivery was relocated: %q", summary)
	}
	if narrative != "" {
		t.Errorf("a project with no accepted delivery was given a narrative: %q", narrative)
	}

	// Re-applying is inert: the runner records versions, but a migration that
	// is not idempotent breaks any converge-with-the-other-line replay.
	if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("0026 is not idempotent: %v", err)
	}
}

func migrationNames(t *testing.T) []string {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// assertDeclarationColumns checks the shape the Go code relies on: NOT NULL
// with a default, so an INSERT that names neither column still produces a row
// scanProject can read.
func assertDeclarationColumns(ctx context.Context, t *testing.T, conn *pgx.Conn, schema string) {
	t.Helper()
	for _, c := range []struct{ name, typ, def string }{
		{"narrative", "text", "''::text"},
		{"declarations", "jsonb", "'{}'::jsonb"},
	} {
		var dataType, isNullable string
		var colDefault *string
		err := conn.QueryRow(ctx, `
			SELECT data_type, is_nullable, column_default
			FROM information_schema.columns
			WHERE table_schema=$1 AND table_name='projects' AND column_name=$2`,
			schema, c.name).Scan(&dataType, &isNullable, &colDefault)
		if err != nil {
			t.Fatalf("projects.%s is missing after migration: %v", c.name, err)
		}
		if dataType != c.typ {
			t.Errorf("projects.%s is %s, want %s", c.name, dataType, c.typ)
		}
		if isNullable != "NO" {
			t.Errorf("projects.%s is nullable — scanProject reads it into a non-pointer", c.name)
		}
		if colDefault == nil || *colDefault != c.def {
			got := "<none>"
			if colDefault != nil {
				got = *colDefault
			}
			t.Errorf("projects.%s default = %s, want %s", c.name, got, c.def)
		}
	}
}

// TestProjectDeclarationsRoundTrip is the store-level contract: a declaration
// document survives a write and a read intact, INCLUDING groups this build has
// no type for, and a later edit of one group leaves the others alone.
func TestProjectDeclarationsRoundTrip(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "decl-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id) })

	created, err := st.CreateProject(ctx, Project{
		ID:      id,
		Name:    "Declared",
		Summary: "What this project is for.",
		Declarations: JSON(`{"datasets":[{"name":"trips","location":"data/trips.csv",` +
			`"naturalKey":"none","keyCandidate":["VendorID","pickup"]}],` +
			`"kpis":[{"id":"on-time","target":0.99}]}`),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.Narrative != "" {
		t.Errorf("a brand-new project was created with a narrative: %q", created.Narrative)
	}

	got, err := st.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	decl, err := ParseDeclarations(got.Declarations)
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decl.Datasets) != 1 || decl.Datasets[0].Location != "data/trips.csv" {
		t.Errorf("datasets did not survive the round trip: %+v", decl.Datasets)
	}

	// Edit one group through the patch path a form would use.
	patch := JSON(`{"environment":{"where":"a laptop"}}`)
	updated, err := st.UpdateProject(ctx, id, ProjectPatch{Declarations: &patch})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(updated.Declarations, &groups); err != nil {
		t.Fatalf("stored declarations are not an object: %v", err)
	}
	for _, g := range []string{"datasets", "kpis", "environment"} {
		if _, ok := groups[g]; !ok {
			t.Errorf("group %q was lost by an edit that did not mention it: %s", g, updated.Declarations)
		}
	}
	if updated.Summary != "What this project is for." {
		t.Errorf("a declarations edit disturbed the human's description: %q", updated.Summary)
	}

	// The slot defaults the detail endpoint hands the recipe form.
	detail, err := st.GetProjectDetail(ctx, id)
	if err != nil {
		t.Fatalf("GetProjectDetail: %v", err)
	}
	// Marcus M-16 residual (2026-09-04): dataset keys are recipe-scoped;
	// the project-level view must not imply that they belong to every output.
	if p, ok := detail.SlotDefaults["keyColumns"]; ok && (p.Value != "" || p.Source != "") {
		t.Errorf("unscoped keyColumns slot default = %+v, want absent or empty", p)
	}
	if p, ok := LookupPrefill(detail.SlotDefaults, "data-quality-audit", "keyColumns"); !ok || p.Value != "none, VendorID, pickup" {
		t.Errorf("data-quality-audit keyColumns slot default = %+v, %t; want the `none, <candidates>` form", p, ok)
	}
	if p, ok := LookupPrefill(detail.SlotDefaults, "data-pipeline", "keyColumns"); ok {
		t.Errorf("data-pipeline inherited dataset keyColumns = %+v, want nothing", p)
	}
	if detail.SlotDefaults["environment"].Value != "a laptop" {
		t.Errorf("environment slot default = %+v", detail.SlotDefaults["environment"])
	}
	if detail.Narrative != "" {
		t.Errorf("a project with no accepted delivery reports a narrative: %q", detail.Narrative)
	}
}

// TestProjectSummaryAndNarrativeAreIndependent is the column-level half of the
// bug fix: writing one must never move the other. The brief-level half is
// TestWriteProjectNeverTouchesSummary in internal/brief.
func TestProjectSummaryAndNarrativeAreIndependent(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "split-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id) })

	mine := "Q1 2026 service telemetry — reliability marts for the SRE team."
	if _, err := st.CreateProject(ctx, Project{ID: id, Name: "Split", Summary: mine}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	narrative := "This project delivered a verified reliability mart over 1.2M events."
	after, err := st.UpdateProject(ctx, id, ProjectPatch{Narrative: &narrative})
	if err != nil {
		t.Fatalf("UpdateProject(narrative): %v", err)
	}
	if after.Summary != mine {
		t.Errorf("writing the narrative overwrote the owner's description: %q", after.Summary)
	}
	if after.Narrative != narrative {
		t.Errorf("narrative = %q, want %q", after.Narrative, narrative)
	}

	edited := "Now with a scorecard."
	after, err = st.UpdateProject(ctx, id, ProjectPatch{Summary: &edited})
	if err != nil {
		t.Fatalf("UpdateProject(summary): %v", err)
	}
	if after.Narrative != narrative {
		t.Errorf("editing the description wiped the narrative: %q", after.Narrative)
	}

	// Both halves reach the list surface, which is what the project card reads.
	list, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	for _, pc := range list {
		if pc.Project != id {
			continue
		}
		if pc.Summary != edited || pc.Narrative != narrative {
			t.Errorf("ListProjects row: summary=%q narrative=%q", pc.Summary, pc.Narrative)
		}
		return
	}
	t.Error("the project did not appear in ListProjects")
}
