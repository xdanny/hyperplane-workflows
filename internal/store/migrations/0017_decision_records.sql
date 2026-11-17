-- Decision records (ADR-style): first-class, self-organizing knowledge.
-- context → decision → why → alternatives → consequences, with refs (edges to
-- related decisions / entities) that feed the knowledge graph (graphify).
-- Additive + idempotent.

CREATE TABLE IF NOT EXISTS decisions (
    id           TEXT PRIMARY KEY,                 -- e.g. "ADR-0011" or a slug
    title        TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'accepted', -- accepted|proposed|superseded|deprecated
    context      TEXT NOT NULL DEFAULT '',
    decision     TEXT NOT NULL DEFAULT '',
    why          TEXT NOT NULL DEFAULT '',
    alternatives TEXT NOT NULL DEFAULT '',
    consequences TEXT NOT NULL DEFAULT '',
    tags         TEXT[] NOT NULL DEFAULT '{}',
    refs         TEXT[] NOT NULL DEFAULT '{}',     -- related ids/entities → graph edges
    source       TEXT NOT NULL DEFAULT '',         -- provenance, e.g. "lab/DECISIONS.md"
    decided_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions (status, updated_at DESC);
