-- Drafts carry the project they were composed under, so a plan that came from
-- "start something in project X" stays attached to X even before it is run.
-- Same project dimension as runs/lanes/decisions. Additive + idempotent.

ALTER TABLE workflow_drafts ADD COLUMN IF NOT EXISTS project TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_workflow_drafts_project
  ON workflow_drafts (project, created_at DESC);
