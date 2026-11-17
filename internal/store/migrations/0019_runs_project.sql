-- Make runs project-aware too (same project dimension as decisions), so the
-- whole platform can be scoped/filtered by project. Additive + idempotent.

ALTER TABLE runs ADD COLUMN IF NOT EXISTS project TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_runs_project ON runs (project, updated_at DESC);
