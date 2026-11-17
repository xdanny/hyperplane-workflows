-- 0007_run_goal: adds a user-supplied goal label to runs so every run row
-- has a meaningful primary identifier independent of the DAG slug.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS goal TEXT;
