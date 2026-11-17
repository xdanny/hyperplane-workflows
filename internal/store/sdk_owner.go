package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// SDKOwnership identifies runs whose liveness is established by a PostgreSQL
// session lock, rather than an operating-system PID on the API's host.
const SDKOwnership = "postgres-session-v1"

// SDKRunOwner serializes checkpoints and health checks on the same connection
// that holds execution ownership. Call Close after final persistence.
type SDKRunOwner struct {
	Context context.Context
	conn    *pgx.Conn
	mu      sync.Mutex
	release func()
	cancel  context.CancelCauseFunc
	runID   string
	sources map[string]bool
}

func (o *SDKRunOwner) Close()            { o.release() }
func (o *SDKRunOwner) SessionID() uint32 { return o.conn.PgConn().PID() }

func (o *SDKRunOwner) SaveCheckpoint(ctx context.Context, runID, project string, owner int, checkpoint JSON, terminal string, output JSON) (string, Event, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, event, err := saveSDKCheckpoint(ctx, o.conn.Begin, runID, project, owner, checkpoint, terminal, output)
	if err != nil && o.conn.IsClosed() {
		o.cancel(fmt.Errorf("SDK ownership connection lost: %w", err))
	}
	return state, event, err
}

// OwnSDKRun holds a dedicated database session until release, including final
// checkpoint persistence. Losing that session cancels the returned work context.
// This requires a direct PostgreSQL connection or session-mode pooler. It uses
// one connection per active run, without exhausting the checkpoint query pool.
func (s *Store) OwnSDKRun(parent context.Context, runID string) (*SDKRunOwner, error) {
	connectCtx, stop := context.WithTimeout(parent, 5*time.Second)
	defer stop()
	conn, err := pgx.ConnectConfig(connectCtx, s.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("SDK ownership connection: %w", err)
	}
	closeConn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = conn.Close(ctx)
	}
	var owned bool
	// Use a separate advisory-lock namespace from migrations. Hash collisions
	// conservatively prevent admission; they can never steal an existing owner.
	err = conn.QueryRow(connectCtx, `SELECT pg_try_advisory_lock(hashtextextended($1, 684795135))`, runID).Scan(&owned)
	if err != nil || !owned {
		closeConn()
		if err == nil {
			err = fmt.Errorf("run already has a live owner")
		}
		return nil, fmt.Errorf("SDK ownership: %w", err)
	}
	ctx, cancel := context.WithCancelCause(parent)
	o := &SDKRunOwner{Context: ctx, conn: conn, cancel: cancel, runID: runID, sources: map[string]bool{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				o.mu.Lock()
				pingCtx, stop := context.WithTimeout(ctx, time.Second)
				// Observe cancellation from any API/CLI process, not only the
				// in-memory owner. The row may not exist yet during admission.
				var terminal bool
				err := conn.QueryRow(pingCtx, `SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=$1 AND state<>'running')`, runID).Scan(&terminal)
				stop()
				o.mu.Unlock()
				if err != nil {
					cancel(fmt.Errorf("SDK ownership connection lost: %w", err))
					return
				}
				if terminal {
					cancel(fmt.Errorf("SDK run is no longer active"))
					return
				}
			}
		}
	}()
	var once sync.Once
	o.release = func() {
		once.Do(func() {
			cancel(context.Canceled)
			<-done
			o.mu.Lock()
			// Close sends Terminate without waiting for the server. Explicitly
			// unlock healthy sessions first so normal release is acknowledged
			// before a new owner/reconciler probes the lock.
			if !conn.IsClosed() {
				unlockCtx, stop := context.WithTimeout(context.Background(), time.Second)
				for source := range o.sources {
					_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,684795135))`, source)
					delete(o.sources, source)
				}
				_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,684795135))`, runID)
				stop()
			}
			closeConn()
			o.mu.Unlock()
		})
	}
	return o, nil
}
