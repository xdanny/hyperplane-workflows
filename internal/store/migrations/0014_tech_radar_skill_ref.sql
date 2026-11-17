-- tech_radar: add skill_ref (link to a skill), description, status, and
-- updated_at so radar entries can be linked to the skill that covers them and
-- upserted idempotently by name.
ALTER TABLE tech_radar
    ADD COLUMN IF NOT EXISTS skill_ref   TEXT,
    ADD COLUMN IF NOT EXISTS description TEXT,
    ADD COLUMN IF NOT EXISTS status      TEXT NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS updated_at  TIMESTAMPTZ NOT NULL DEFAULT now();

-- Unique constraint on tech name so UpsertTechRadarEntry is idempotent.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tech_radar_tech ON tech_radar (tech);
