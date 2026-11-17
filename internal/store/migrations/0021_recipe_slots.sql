-- Recipes: a workflow template becomes declarative policy over a fixed
-- lifecycle. Orchestration does not vary — three things do:
--
--   context_slots  WHAT MUST BE KNOWN before the workflow may start
--   criteria       WHAT MUST BE PROVEN before the deliverable counts as done
--   spec           WHAT IS PRODUCED (already present)
--
-- Both are JSONB arrays so a recipe can be edited without a schema change.
-- context_slots elements: {id, label, hint, required, example}
-- criteria elements:      {id, title, hint}
--
-- Runs already carry a `doc` JSONB (0001_init.sql); an instantiated recipe
-- stamps {recipe, slots, criteria} into it, so no run-side column is needed.

ALTER TABLE workflow_templates
    ADD COLUMN IF NOT EXISTS context_slots JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS criteria      JSONB NOT NULL DEFAULT '[]';
