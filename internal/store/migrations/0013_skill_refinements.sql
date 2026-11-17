-- skill_refinements: a durable, queryable lineage of every prompt refinement the
-- optimize ("DSPy") loop tries for a skill, each with its eval score. This is the
-- per-PROMPT history (optimize_runs is the per-RUN summary): one row per baseline /
-- variant / winner, so you can see how a skill's instruction improved over time and
-- which scored what. The winning, applied refinement is flagged.
CREATE TABLE IF NOT EXISTS skill_refinements (
    id              TEXT PRIMARY KEY,
    skill_id        TEXT NOT NULL,
    optimize_run_id TEXT,                         -- the optimize_runs row this came from
    role            TEXT NOT NULL,                -- 'baseline' | 'variant' | 'winner'
    instruction     TEXT NOT NULL,                -- the prompt text of this refinement
    pass_rate       DOUBLE PRECISION NOT NULL,    -- its eval pass-rate
    mean_score      DOUBLE PRECISION NOT NULL,    -- its eval mean (judge) score
    gain            DOUBLE PRECISION NOT NULL DEFAULT 0,  -- pass-rate gain over baseline (winner only)
    applied         BOOLEAN NOT NULL DEFAULT FALSE,       -- persisted back to the skill?
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_skill_refinements_skill ON skill_refinements (skill_id, created_at DESC);
