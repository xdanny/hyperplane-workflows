package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SaveSDKCheckpoint atomically updates the run snapshot, its single delivery
// record and the journal. The run row fences cancellation and stale owners;
// no model call is made while this transaction holds a lock. terminal is empty
// for a checkpoint, otherwise passed, failed or interrupted after worker exit.
func (s *Store) SaveSDKCheckpoint(ctx context.Context, runID, project string, owner int, checkpoint JSON, terminal string, output JSON) (string, Event, error) {
	return saveSDKCheckpoint(ctx, s.Pool.Begin, runID, project, owner, checkpoint, terminal, output)
}

func saveSDKCheckpoint(ctx context.Context, begin func(context.Context) (pgx.Tx, error), runID, project string, owner int, checkpoint JSON, terminal string, output JSON) (string, Event, error) {
	tx, err := begin(ctx)
	if err != nil {
		return "", Event{}, err
	}
	defer tx.Rollback(ctx)
	state, event, err := saveSDKCheckpointTx(ctx, tx, runID, project, owner, checkpoint, terminal, output)
	if err != nil {
		return "", Event{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Event{}, err
	}
	return state, event, nil
}

// The caller owns the transaction, allowing continuation admission and its
// first checkpoint to share the existing ownership and cancellation fences.
func saveSDKCheckpointTx(ctx context.Context, tx pgx.Tx, runID, project string, owner int, checkpoint JSON, terminal string, output JSON) (string, Event, error) {
	if terminal != "" && terminal != "passed" && terminal != "failed" && terminal != "interrupted" {
		return "", Event{}, fmt.Errorf("invalid SDK terminal state")
	}
	var err error
	var state string
	var committed JSON
	err = tx.QueryRow(ctx, `SELECT state,COALESCE(doc->'sdk','{}'::jsonb) FROM runs
 WHERE run_id=$1 AND project=$2 AND owner_pid=$3 AND options->>'runtime'='codex-sdk'
 AND (COALESCE(options->>'ownership','') <> 'postgres-session-v1' OR (
   options->>'ownerSession'=pg_backend_pid()::text AND EXISTS (
     SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND objsubid=1
     AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
     AND classid::bigint=((hashtextextended($1,684795135) >> 32) & 4294967295)
     AND objid::bigint=(hashtextextended($1,684795135) & 4294967295))))
 FOR UPDATE`, runID, project, owner).Scan(&state, &committed)
	if err == pgx.ErrNoRows {
		return "", Event{}, ErrNotRunning
	}
	if err != nil {
		return "", Event{}, err
	}
	if state != "running" {
		if state != "interrupted" || terminal == "" {
			return "", Event{}, ErrNotRunning
		}
		// Cancellation/recovery already committed the controlling receipt. A late
		// worker completion returns it without overwriting evidence or adding events.
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT seq FROM events WHERE run_id=$1 AND type='run.done' ORDER BY seq DESC LIMIT 1`, runID).Scan(&seq); err != nil {
			return "", Event{}, err
		}
		return state, Event{RunID: runID, Seq: seq, Type: "run.done", Node: "delivery", Level: "info", Data: committed}, nil
	}
	err = tx.QueryRow(ctx, `UPDATE runs SET
   doc=doc || jsonb_build_object('sdk',$2::jsonb),updated_at=now(),
   state=CASE WHEN $3='' THEN state ELSE $3 END,
   ended_at=CASE WHEN $3<>'' THEN now() ELSE ended_at END,
   summary=CASE WHEN $3<>'' THEN $2::jsonb ELSE summary END
   WHERE run_id=$1 RETURNING state,doc->'sdk'`, runID, jsonOr(checkpoint), terminal).Scan(&state, &checkpoint)
	if err != nil {
		return "", Event{}, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO nodes (run_id,node_id,kind,role,state,attempt,error,output,doc)
		VALUES ($1,'delivery','sdk','generate',$2,COALESCE(($3::jsonb->>'attempts')::int,($3::jsonb->>'attempt')::int+1,0),
		COALESCE($3::jsonb->>'error',''),CASE WHEN $2='interrupted' THEN '{}'::jsonb ELSE $4::jsonb END,$3)
		ON CONFLICT (run_id,node_id) DO UPDATE SET state=EXCLUDED.state,attempt=EXCLUDED.attempt,
		error=EXCLUDED.error,output=EXCLUDED.output,doc=EXCLUDED.doc,updated_at=now()`, runID, state, jsonOr(checkpoint), jsonOr(output))
	if err != nil {
		return "", Event{}, err
	}
	var snapshot struct {
		Phase    string          `json:"phase"`
		Approval json.RawMessage `json:"approval"`
	}
	if err := json.Unmarshal(checkpoint, &snapshot); err != nil {
		return "", Event{}, err
	}
	if terminal == "" && snapshot.Phase == "approval_pending" {
		var approval struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(snapshot.Approval, &approval); err != nil || approval.ID == "" {
			return "", Event{}, fmt.Errorf("approval evidence is required")
		}
		_, err = tx.Exec(ctx, `INSERT INTO records(type,id,state,gate_key,run_id,kind,consumed,doc)
    VALUES('approvals',$1,'pending',$1,$2,'sdk-approval',false,$3)`, approval.ID, runID, snapshot.Approval)
		if err != nil {
			return "", Event{}, err
		}
	}
	if terminal != "" {
		if err := finishSDKTicket(ctx, tx, runID, state, checkpoint); err != nil {
			return "", Event{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE records SET state='dismissed',updated_at=now() WHERE run_id=$1 AND kind='sdk-approval' AND state='pending'`, runID)
		if err != nil {
			return "", Event{}, err
		}
	}

	kind := "sdk.checkpoint"
	if terminal != "" {
		kind = "run.done"
	}
	var seq int64
	err = tx.QueryRow(ctx, `INSERT INTO events (run_id,seq,type,node,data)
		VALUES ($1,(SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1),$2,'delivery',$3)
		RETURNING seq`, runID, kind, jsonOr(checkpoint)).Scan(&seq)
	if err != nil {
		return "", Event{}, err
	}
	return state, Event{RunID: runID, Seq: seq, Type: kind, Node: "delivery", Level: "info", Data: checkpoint}, nil
}
