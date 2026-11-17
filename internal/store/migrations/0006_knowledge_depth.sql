CREATE TABLE IF NOT EXISTS tech_radar (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tech      TEXT NOT NULL,
    quadrant  TEXT NOT NULL CHECK (quadrant IN ('languages', 'tools', 'platforms', 'techniques')),
    ring      TEXT NOT NULL CHECK (ring IN ('adopt', 'trial', 'assess', 'hold')),
    note      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS stacks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    runtime     TEXT,
    dockerized  BOOLEAN NOT NULL DEFAULT false,
    notes       TEXT,
    files       JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS categories (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'all' CHECK (kind IN ('skills', 'processes', 'workflows', 'all')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workflow_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    spec        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
