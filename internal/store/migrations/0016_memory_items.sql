-- Phase 3: memory_items table for platform-wide typed facts.
-- Additive + idempotent (IF NOT EXISTS everywhere).

CREATE TABLE IF NOT EXISTS memory_items (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('user-pref','project-fact','feedback','reference')),
    key        TEXT NOT NULL,
    value      JSONB NOT NULL DEFAULT '{}',
    source     TEXT NOT NULL DEFAULT 'agent',
    run_id     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_key  ON memory_items (key);
CREATE INDEX        IF NOT EXISTS idx_memory_kind ON memory_items (kind, updated_at DESC);
