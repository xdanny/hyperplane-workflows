-- Personas: the audit charters in personas/*.md become first-class platform
-- citizens — registered reviewers the platform can invoke, not just files a
-- human pastes into a chat session.
--
-- The charter TEXT is stored in full because it IS the reviewer: the review
-- prompt is assembled from its own sections (IDENTITY, RULES, the protocols),
-- so a review run from the API and a review run by hand read the same brief.
-- The repo stays the source of truth — `make seed` refreshes content the same
-- way templates and processes are refreshed, and there is no write API.
--
-- competence is the parsed COMPETENCE section: {skills[], processes[],
-- domains[], wording} — the routing hook a future auto-trigger (spec AI-12)
-- uses to ask "who is qualified to judge this skill?".
--
-- Additive + idempotent.

CREATE TABLE IF NOT EXISTS personas (
    id         TEXT PRIMARY KEY,                         -- slug from the filename ("dev-ml-engineer")
    name       TEXT NOT NULL,                            -- the charter's H1 ("Dev — senior ML engineer")
    bar        TEXT NOT NULL DEFAULT '',                 -- the reviewer's stated bar, in their own words
    charter    TEXT NOT NULL DEFAULT '',                 -- the full markdown, verbatim
    competence JSONB NOT NULL DEFAULT '{}'::jsonb,       -- {skills[],processes[],domains[],wording}
    version    TEXT NOT NULL DEFAULT '',                 -- content hash of the charter
    source     TEXT NOT NULL DEFAULT '',                 -- "personas/dev-ml-engineer.md"
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_personas_updated ON personas (updated_at DESC);

-- Persona reviews live in `records` (type='persona-review'), insert-only, with
-- the reviewed target in the doc. This index keeps "what did the personas say
-- about THIS skill / THIS run?" a single lookup rather than a table scan.
CREATE INDEX IF NOT EXISTS idx_records_persona_review_target
    ON records ((doc->>'target'))
    WHERE type = 'persona-review';
