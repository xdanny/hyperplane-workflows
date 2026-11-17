-- Outcome briefs: the plain-language translation of what a run, a delivery or a
-- project ACTUALLY produced, written by the platform AFTER the fact.
--
-- Every piece of prose Hyperplane holds today was written by a node INSIDE the
-- DAG — a summary node runs before the review gate, before the repair, before
-- the human accepts or rejects — so no text in the platform is in a position to
-- state a run's true outcome. It cannot be: the outcome had not happened yet.
-- A brief is written once the subject is terminal, from authoritative state
-- (run state, failing node, gate verdicts, criteria and their proof, the
-- scorecard rows, the repair lineage, the recorded delivery verdict), and it is
-- therefore the only prose here that can say what a reader actually got.
--
-- INSERT-ONLY, the same as records and persona reviews. The latest row wins for
-- display; the earlier ones stay because a brief written before a repair and
-- the one written after are exactly the pair a reader wants both halves of.
--
-- `body` holds the four-part brief as a JSON document —
-- {whatItBuilds, whatHappened, trueOutcome, nextSteps, numbersUsed[]} — which is
-- the shape schemas/brief.json asks the engine for and the shape
-- internal/brief renders. It is TEXT rather than jsonb on purpose: nothing
-- queries into it, and a fallback brief must be storable even if it is prose.
--
-- `grounded` is the honesty flag. Every number in a generated brief is checked
-- back against the state document it was given; a brief that still carries a
-- number the state does not support is stored with grounded=false and the
-- offenders listed in `ungrounded_numbers`, so the UI can badge it rather than
-- the platform quietly shipping an invented figure. It is never a reason to
-- withhold the brief.
--
-- Additive + idempotent.

CREATE TABLE IF NOT EXISTS briefs (
    id                 TEXT PRIMARY KEY,
    subject_kind       TEXT NOT NULL,
    subject_id         TEXT NOT NULL,
    project            TEXT NOT NULL DEFAULT '',
    body               TEXT NOT NULL,
    grounded           BOOLEAN NOT NULL,
    -- The numeric tokens the grounding check could not find in the state
    -- document, SPACE-separated (a numeric token can contain a comma —
    -- "9,412" — so a comma is not a separator here). Empty when grounded.
    ungrounded_numbers TEXT NOT NULL DEFAULT '',
    -- 'codex' when the engine wrote it, 'fallback' when the platform did.
    engine             TEXT NOT NULL,
    -- sha256 of the state document the brief was written from, so a reader can
    -- tell whether two briefs of the same subject saw the same facts.
    inputs_digest      TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'briefs'::regclass AND conname = 'briefs_subject_kind_check'
  ) THEN
    ALTER TABLE briefs ADD CONSTRAINT briefs_subject_kind_check
      CHECK (subject_kind IN ('run', 'delivery', 'project'));
  END IF;
END $$;

-- "the latest brief for this subject" is the only read that matters for
-- display, so it is a single index seek rather than a scan plus a sort.
CREATE INDEX IF NOT EXISTS idx_briefs_subject
    ON briefs (subject_kind, subject_id, created_at DESC);

-- "the accepted deliveries of this project, newest first" — what the project
-- narrative is regenerated from.
CREATE INDEX IF NOT EXISTS idx_briefs_project
    ON briefs (project, created_at DESC) WHERE project <> '';
