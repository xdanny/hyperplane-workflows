-- Hyperplane initial schema.
-- Postgres is the source of truth for runs/state and the knowledge base.
-- Per-run folders remain the on-disk projection for artifacts and logs.

-- pg_trgm powers fuzzy/full-text search over the knowledge tables.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------------------
-- Execution: runs, nodes, events
-- ---------------------------------------------------------------------------

CREATE TABLE runs (
    run_id      TEXT PRIMARY KEY,
    dag         TEXT NOT NULL,
    state       TEXT NOT NULL,                 -- running|passed|failed|blocked|interrupted
    mode        TEXT,                          -- run|plan
    type        TEXT,                          -- automation|docs-plans|asset-generation|...
    domain      TEXT,
    risk_level  TEXT,
    parent_run  TEXT REFERENCES runs(run_id) ON DELETE SET NULL,  -- sub-DAG / process call nesting
    started_at  TIMESTAMPTZ,
    ended_at    TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    owner_pid   INTEGER,                       -- orphan reconciliation
    options     JSONB NOT NULL DEFAULT '{}',
    summary     JSONB NOT NULL DEFAULT '{}',
    metadata    JSONB NOT NULL DEFAULT '{}',
    doc         JSONB NOT NULL DEFAULT '{}'    -- full status snapshot
);
CREATE INDEX idx_runs_updated ON runs (updated_at DESC);
CREATE INDEX idx_runs_state ON runs (state);
CREATE INDEX idx_runs_parent ON runs (parent_run);

CREATE TABLE nodes (
    run_id      TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    node_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,                 -- agent|shell|fanout|verify|gate|connector|process
    role        TEXT,
    state       TEXT NOT NULL,                 -- pending|running|passed|failed|regen|skipped|blocked
    attempt     INTEGER NOT NULL DEFAULT 0,
    score       DOUBLE PRECISION,
    error       TEXT,
    output      JSONB,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    doc         JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (run_id, node_id)
);
CREATE INDEX idx_nodes_run ON nodes (run_id);

CREATE TABLE events (
    run_id  TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    seq     BIGINT NOT NULL,
    ts      TIMESTAMPTZ NOT NULL DEFAULT now(),
    level   TEXT NOT NULL DEFAULT 'info',
    type    TEXT NOT NULL,
    node    TEXT,
    data    JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (run_id, seq)
);
CREATE INDEX idx_events_run ON events (run_id, seq);

-- Records: approvals, selections, notifications, proposals, drafts.
CREATE TABLE records (
    type        TEXT NOT NULL,                 -- approvals|notifications|proposals|drafts
    id          TEXT NOT NULL,
    state       TEXT,                          -- pending|approved|rejected|closed
    gate_key    TEXT,                          -- approval dedup across reruns
    run_id      TEXT,
    kind        TEXT,                          -- approval|selection|notification|proposal|draft
    consumed    BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    doc         JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (type, id)
);
CREATE INDEX idx_records_type_state ON records (type, state);
CREATE INDEX idx_records_gatekey ON records (type, gate_key);
CREATE INDEX idx_records_updated ON records (type, updated_at DESC);

-- ---------------------------------------------------------------------------
-- Knowledge base: skills, context bundles, trusted processes, connectors
-- ---------------------------------------------------------------------------

CREATE TABLE skills (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    category    TEXT,
    reasoning   TEXT,
    instruction TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    search      TEXT GENERATED ALWAYS AS (
        coalesce(name,'') || ' ' || coalesce(description,'') || ' ' || coalesce(category,'')
    ) STORED
);
CREATE INDEX idx_skills_search_trgm ON skills USING gin (search gin_trgm_ops);
CREATE INDEX idx_skills_category ON skills (category);

CREATE TABLE context_bundles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    items       JSONB NOT NULL DEFAULT '[]',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Trusted Process Registry: versioned, typed, callable sub-workflows.
CREATE TABLE processes (
    id          TEXT NOT NULL,
    version     TEXT NOT NULL,                 -- semver
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'draft', -- draft|trusted|deprecated
    contract    JSONB NOT NULL DEFAULT '{}',   -- {inputs: schema, outputs: schema}
    spec        JSONB NOT NULL DEFAULT '{}',   -- the sub-DAG node list
    fixtures    JSONB NOT NULL DEFAULT '{}',
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    search      TEXT GENERATED ALWAYS AS (
        coalesce(name,'') || ' ' || coalesce(description,'')
    ) STORED,
    PRIMARY KEY (id, version)
);
CREATE INDEX idx_processes_status ON processes (status);
CREATE INDEX idx_processes_search_trgm ON processes USING gin (search gin_trgm_ops);

CREATE TABLE connectors (
    id          TEXT NOT NULL,
    version     TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'draft',
    manifest    JSONB NOT NULL DEFAULT '{}',   -- typed in/out, side-effects, secret refs, fixtures
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, version)
);

-- ---------------------------------------------------------------------------
-- Learning: DSPy/RLHF preferences captured from selection gates
-- ---------------------------------------------------------------------------

CREATE TABLE preferences (
    id          BIGSERIAL PRIMARY KEY,
    skill_id    TEXT NOT NULL,
    source      TEXT NOT NULL,                 -- human-pick|judge
    winner_id   TEXT,
    candidates  JSONB NOT NULL DEFAULT '[]',
    run_id      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_preferences_skill ON preferences (skill_id, created_at DESC);
