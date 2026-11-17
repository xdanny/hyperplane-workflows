-- Scope knowledge by project: decisions belong to a project (e.g. "hyperplane",
-- "lab", an app). Empty project = unscoped/global. Additive + idempotent.

ALTER TABLE decisions ADD COLUMN IF NOT EXISTS project TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_decisions_project ON decisions (project, updated_at DESC);
