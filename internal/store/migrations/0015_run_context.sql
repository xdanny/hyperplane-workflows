-- Phase 2: run_context table for context carryover within and across runs.
-- Additive + idempotent (IF NOT EXISTS everywhere).

CREATE TABLE IF NOT EXISTS run_context (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    node_id     TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('decision','artifact','fact','feedback')),
    key         TEXT NOT NULL,
    value       JSONB NOT NULL DEFAULT '{}',
    tags        TEXT[] NOT NULL DEFAULT '{}',
    source_run  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_run_context_run  ON run_context (run_id, node_id);
CREATE INDEX IF NOT EXISTS idx_run_context_key  ON run_context (key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_run_context_tags ON run_context USING GIN (tags);
