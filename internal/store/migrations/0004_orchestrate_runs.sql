-- orchestrate_runs: records a self-driving backlog-orchestration loop invocation.
CREATE TABLE IF NOT EXISTS orchestrate_runs (
    id           TEXT PRIMARY KEY,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    backlog_size INTEGER NOT NULL,
    max_items    INTEGER NOT NULL,
    done         INTEGER NOT NULL DEFAULT 0,
    failed       INTEGER NOT NULL DEFAULT 0,
    items        JSONB NOT NULL DEFAULT '[]',
    status       TEXT NOT NULL DEFAULT 'running'
);
CREATE INDEX IF NOT EXISTS idx_orchestrate_runs_started ON orchestrate_runs (started_at DESC);
