-- evalsets: store eval cases as JSONB
CREATE TABLE IF NOT EXISTS evalsets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    skill_ref TEXT,
    cases JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- eval_runs: results of running an evalset
CREATE TABLE IF NOT EXISTS eval_runs (
    id TEXT PRIMARY KEY,
    evalset_id TEXT NOT NULL REFERENCES evalsets(id) ON DELETE CASCADE,
    target TEXT NOT NULL,
    engine TEXT NOT NULL DEFAULT 'codex',
    model TEXT,
    started_at TIMESTAMPTZ DEFAULT now(),
    ended_at TIMESTAMPTZ,
    results JSONB NOT NULL DEFAULT '[]',
    pass_rate DOUBLE PRECISION,
    mean_score DOUBLE PRECISION,
    status TEXT NOT NULL DEFAULT 'running'
);
