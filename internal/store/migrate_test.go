package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Concurrent API/CLI startup must not race while applying the initial schema.
func TestConcurrentFreshMigrations(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	schema := fmt.Sprintf("migration_probe_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := st.Pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer st.Pool.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
	cfg := st.Pool.Config()
	if _, err := st.Pool.Exec(ctx, "CREATE TABLE "+quoted+".schema_migrations(version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ", public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	probe := &Store{Pool: pool}
	ready := make(chan struct{})
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { <-ready; _, err := probe.Migrate(ctx); results <- err }()
	}
	close(ready)
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent migration: %v", err)
		}
	}
}
