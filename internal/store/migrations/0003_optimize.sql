-- optimize_runs: records of each skill self-improvement loop invocation.
CREATE TABLE IF NOT EXISTS optimize_runs (
    id                    TEXT PRIMARY KEY,
    skill_id              TEXT NOT NULL,
    evalset_id            TEXT NOT NULL,
    baseline_pass_rate    DOUBLE PRECISION NOT NULL DEFAULT 0,
    baseline_mean_score   DOUBLE PRECISION NOT NULL DEFAULT 0,
    winner_instruction    TEXT,       -- NULL when no variant beat baseline
    gain                  DOUBLE PRECISION NOT NULL DEFAULT 0,
    applied               BOOLEAN NOT NULL DEFAULT false,
    variants              JSONB NOT NULL DEFAULT '[]',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_optimize_runs_skill ON optimize_runs (skill_id, created_at DESC);
