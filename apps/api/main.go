// Command api is the Hyperplane HTTP server: it applies migrations, then serves
// the loopback-only API. Local-first, single user, no auth.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/config"
	"github.com/xdanny/hyperplane-workflows/internal/envfile"
	"github.com/xdanny/hyperplane-workflows/internal/httpapi"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func main() {
	envfile.Load(".env") // pick up gitignored local secrets (e.g. CONTEXT7_API_KEY)
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	applied, err := st.Migrate(ctx)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if len(applied) > 0 {
		log.Printf("applied migrations: %v", applied)
	}

	// Preserve runs with a live database-session owner, including CLI/other-host
	// work. Abandoned runs become interrupted; startup never replays their effects.
	if n, err := st.ReconcileOrphans(ctx); err != nil {
		log.Printf("reconcile orphans: %v", err)
	} else if n > 0 {
		log.Printf("reconciled %d orphaned run(s) -> interrupted", n)
	}

	// A fresh install has no templates until something seeds them, and the
	// launcher never ran `make seed`. One line either way, so the log says what
	// was decided rather than leaving an empty Templates panel unexplained.
	log.Print(httpapi.SeedDefaultsOnStartup(ctx, st))

	api := httpapi.New(cfg, st)

	// The self-improvement pass runs on a timer inside this process (M26 built
	// the pass; nothing ever ran it). HYPERPLANE_IMPROVE_INTERVAL=0 turns the
	// timer off — the manual trigger keeps working either way.
	api.StartLearning(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("hyperplane api listening on http://%s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	if err := api.Shutdown(shutdownCtx); err != nil {
		log.Printf("workflow shutdown: %v", err)
	}
	_ = os.Stdout.Sync()
}
