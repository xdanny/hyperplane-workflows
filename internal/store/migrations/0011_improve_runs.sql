-- improve_runs: records each always-on self-improvement loop invocation.
CREATE TABLE IF NOT EXISTS improve_runs (
    id                  TEXT PRIMARY KEY,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    skills_considered   INTEGER NOT NULL DEFAULT 0,
    skills_optimized    INTEGER NOT NULL DEFAULT 0,
    skills_promoted     INTEGER NOT NULL DEFAULT 0,
    items               JSONB NOT NULL DEFAULT '[]',
    status              TEXT NOT NULL DEFAULT 'running'
);
CREATE INDEX IF NOT EXISTS idx_improve_runs_started ON improve_runs (started_at DESC);
