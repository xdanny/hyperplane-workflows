-- First-class projects. Until now a project was only a free-text TAG on runs,
-- lanes and decisions; this gives it a row so it can carry a display name, a
-- summary and a status, and so a project that has no items yet still exists.
--
-- Deliberately CONVERGING with the data-engineering line, which created the
-- same table in its own 0022_projects_products.sql: same table name, same
-- column types, same constraint names. A database that already carries that
-- schema applies this migration as a no-op, which is why every statement here
-- is IF NOT EXISTS or guarded. Never edit this into a shape that conflicts.
--
-- What is deliberately NOT ported: the products table and the NOT VALID
-- foreign keys from runs/lanes/decisions. Main keeps TAG semantics — the
-- project column is free text and '' means unscoped — and this table is a
-- projection kept in sync by store.EnsureProject.

CREATE TABLE IF NOT EXISTS projects (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  summary    TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'active',
  owner      TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Converge a table that already exists with a narrower shape (and add
-- `provenance`, which the other line introduced separately in its
-- 0030_project_provenance.sql).
ALTER TABLE projects ADD COLUMN IF NOT EXISTS summary    TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS status     TEXT NOT NULL DEFAULT 'active';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS owner      TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS provenance TEXT NOT NULL DEFAULT 'user';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE projects ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Constraints have no IF NOT EXISTS, so they are added by name check. The
-- empty-id check is what keeps the unscoped tag ('') from ever becoming a row.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'projects'::regclass AND conname = 'projects_provenance_check'
  ) THEN
    ALTER TABLE projects ADD CONSTRAINT projects_provenance_check
      CHECK (provenance IN ('user', 'demo', 'imported', 'test'));
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'projects'::regclass AND conname = 'projects_nonempty_id'
  ) THEN
    ALTER TABLE projects ADD CONSTRAINT projects_nonempty_id CHECK (id <> '') NOT VALID;
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS projects_human_workspace_idx
  ON projects (updated_at DESC, id) WHERE provenance <> 'test';

-- Backfill from the tags that already exist, with the same name derivation the
-- other line used, so an existing database gets its projects the moment this
-- applies. ON CONFLICT DO NOTHING keeps re-application inert.
INSERT INTO projects (id, name)
SELECT project, INITCAP(REPLACE(project, '-', ' '))
FROM (
  SELECT DISTINCT project FROM runs      WHERE project <> ''
  UNION
  SELECT DISTINCT project FROM decisions WHERE project <> ''
  UNION
  SELECT DISTINCT project FROM lanes     WHERE project <> ''
) tagged
ON CONFLICT (id) DO NOTHING;
