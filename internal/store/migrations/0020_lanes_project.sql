-- Project-scope the Board subsystem (same project dimension as decisions/runs)
-- and link lanes back to the run they were decomposed from, if any. Tickets
-- inherit project via their lane — no ticket-level column. Additive + idempotent.

ALTER TABLE lanes ADD COLUMN IF NOT EXISTS project TEXT NOT NULL DEFAULT '';
ALTER TABLE lanes ADD COLUMN IF NOT EXISTS origin_run TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_lanes_project ON lanes (project, created_at DESC);
