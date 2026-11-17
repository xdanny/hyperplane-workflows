package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrContinuationChanged = errors.New("continuation source changed; review it again")

// ErrContinuationCommitUnknown means commit was attempted but not acknowledged.
// Preserve the successor workspace: the database may already refer to its pins.
var ErrContinuationCommitUnknown = errors.New("continuation commit outcome is unknown")

// SDKContinuationExists is an idempotent replay of an already committed claim.
// The caller returns this successor instead of launching another worker.
type SDKContinuationExists struct{ RunID string }

func (e *SDKContinuationExists) Error() string { return "continuation already started: " + e.RunID }

// SDKContinuationRevision binds reviewed execution input and the controlling
// checkpoint. A claim added to doc.continuation does not change this revision.
func SDKContinuationRevision(run Run) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(jsonOr(run.Doc), &doc); err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		RunID      string `json:"runId"`
		State      string `json:"state"`
		Project    string `json:"project"`
		Options    JSON   `json:"options"`
		Checkpoint JSON   `json:"checkpoint"`
	}{run.RunID, run.State, run.Project, jsonOr(run.Options), jsonOr(doc["sdk"])})
	if err != nil {
		return "", err
	}
	// PostgreSQL JSONB may reorder keys. Canonicalize without converting exact
	// numeric declarations through float64.
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return "", err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// HoldContinuationSource fences copying against another continuation, a live
// source worker and deletion. The heartbeat continues to monitor the child ID.
func (o *SDKRunOwner) HoldContinuationSource(ctx context.Context, sourceID string) (func(), error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.Context.Err(); err != nil {
		return nil, err
	}
	if sourceID == "" || sourceID == o.runID || o.sources[sourceID] {
		return nil, ErrContinuationChanged
	}
	var held bool
	if err := o.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,684795135))`, sourceID).Scan(&held); err != nil {
		return nil, err
	}
	if !held {
		return nil, ErrRunActive
	}
	o.sources[sourceID] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			defer o.mu.Unlock()
			if !o.sources[sourceID] {
				return
			}
			delete(o.sources, sourceID)
			if o.conn.IsClosed() {
				return
			}
			releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var released bool
			if err := o.conn.QueryRow(releaseCtx, `SELECT pg_advisory_unlock(hashtextextended($1,684795135))`, sourceID).Scan(&released); err != nil || !released {
				o.cancel(fmt.Errorf("SDK continuation source ownership release failed"))
			}
		})
	}, nil
}

// StartContinuation atomically records the child, its first checkpoint and the
// source's one successor claim. Files must already have been copied and verified
// under HoldContinuationSource. No model is invoked inside this transaction.
func (o *SDKRunOwner) StartContinuation(ctx context.Context, sourceID, reviewToken string, child Run, initialCheckpoint JSON) (Event, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.Context.Err(); err != nil {
		return Event{}, err
	}
	if sourceID == "" || sourceID == o.runID || child.RunID != o.runID || !o.sources[sourceID] {
		return Event{}, ErrContinuationChanged
	}
	tx, err := o.conn.Begin(ctx)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(ctx)
	// Verify actual session locks, not only in-memory lease bookkeeping.
	for _, id := range []string{sourceID, child.RunID} {
		var held bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
   SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND objsubid=1
   AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
   AND classid::bigint=((hashtextextended($1,684795135) >> 32) & 4294967295)
   AND objid::bigint=(hashtextextended($1,684795135) & 4294967295))`, id).Scan(&held); err != nil {
			return Event{}, err
		}
		if !held {
			return Event{}, ErrContinuationChanged
		}
	}
	var source Run
	source.RunID = sourceID
	if err = tx.QueryRow(ctx, `SELECT state,project,options,doc FROM runs WHERE run_id=$1 FOR UPDATE`, sourceID).Scan(&source.State, &source.Project, &source.Options, &source.Doc); err == pgx.ErrNoRows {
		return Event{}, ErrNotFound
	} else if err != nil {
		return Event{}, err
	}
	revision, err := SDKContinuationRevision(source)
	if err != nil {
		return Event{}, err
	}
	var sourceOptions struct {
		Runtime string `json:"runtime"`
		Request struct {
			Workflow string `json:"workflow"`
		} `json:"request"`
	}
	if err = json.Unmarshal(source.Options, &sourceOptions); err != nil {
		return Event{}, err
	}
	if source.State != "interrupted" || sourceOptions.Runtime != "codex-sdk" || sourceOptions.Request.Workflow != "node-workflow" || reviewToken != revision || child.Project != source.Project {
		return Event{}, ErrContinuationChanged
	}
	var doc struct {
		Continuation *struct {
			RunID       string `json:"runId"`
			ReviewToken string `json:"reviewToken"`
		} `json:"continuation"`
	}
	if err = json.Unmarshal(source.Doc, &doc); err != nil {
		return Event{}, err
	}
	if doc.Continuation != nil {
		if doc.Continuation.RunID != "" && doc.Continuation.ReviewToken == reviewToken {
			return Event{}, &SDKContinuationExists{RunID: doc.Continuation.RunID}
		}
		return Event{}, ErrContinuationChanged
	}
	var childOptions struct {
		Runtime      string `json:"runtime"`
		Ownership    string `json:"ownership"`
		OwnerSession uint32 `json:"ownerSession"`
		Request      struct {
			Workflow string `json:"workflow"`
		} `json:"request"`
	}
	if err = json.Unmarshal(child.Options, &childOptions); err != nil {
		return Event{}, err
	}
	if child.State != "running" || childOptions.Runtime != "codex-sdk" || childOptions.Ownership != SDKOwnership || childOptions.OwnerSession != o.SessionID() || childOptions.Request.Workflow != "node-workflow" {
		return Event{}, ErrContinuationChanged
	}
	child.ParentRun = sourceID
	_, err = tx.Exec(ctx, `INSERT INTO runs (run_id,dag,goal,state,mode,type,domain,risk_level,parent_run,started_at,ended_at,updated_at,owner_pid,options,summary,metadata,doc,project)
 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,now(),$12,$13,$14,$15,$16,$17)`, child.RunID, child.DAG, child.Goal, child.State, child.Mode, child.Type, child.Domain, child.RiskLevel, child.ParentRun, timePtr(child.StartedAt), timePtr(child.EndedAt), child.OwnerPID, jsonOr(child.Options), jsonOr(child.Summary), jsonOr(child.Metadata), jsonOr(child.Doc), child.Project)
	if err != nil {
		return Event{}, err
	}
	_, event, err := saveSDKCheckpointTx(ctx, tx, child.RunID, child.Project, child.OwnerPID, initialCheckpoint, "", nil)
	if err != nil {
		return Event{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE runs SET doc=doc||jsonb_build_object('continuation',jsonb_build_object('runId',$2::text,'reviewToken',$3::text)),updated_at=now() WHERE run_id=$1`, sourceID, child.RunID, reviewToken); err != nil {
		return Event{}, err
	}
	if err = o.Context.Err(); err != nil {
		return Event{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrContinuationCommitUnknown, err)
	}
	return event, nil
}
