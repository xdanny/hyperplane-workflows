-- Project declarations, and the separation of the two sentences a project page
-- shows.
--
-- 1. `narrative` — "WHAT THIS PROJECT HAS BUILT SO FAR". Machine-written by
--    internal/brief from the briefs of the project's ACCEPTED deliveries.
--    Until now that writer wrote into `summary`, which is the human's "what
--    this project is for" field, so accepting a delivery overwrote the owner's
--    description, and a project with zero runs rendered the owner's typed
--    intent under a heading claiming it was a result. `summary` goes back to
--    being purely human-owned; nothing but a person ever writes it again.
--
-- 2. `declarations` — the facts a project carries ONCE instead of being
--    re-typed into every run's slots: the purpose block, the datasets it works
--    on, the environment its runs execute in. An extensible document of typed
--    GROUPS: readers take the groups they know and leave the rest alone, so a
--    later group (kpis[], evalsets[]) needs no migration and no code change in
--    anything that only round-trips the column.
--
-- Declarations hold REFERENCES only — a path, a table name, an env var name —
-- never a credential and never a value. Same rule as connector credentialsRef.
--
-- Additive + idempotent, and safe on a database that already has 0025.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS narrative    TEXT  NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS declarations JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Move an already-written narrative out of the human's field.
--
-- A database written by the previous build has the machine narrative sitting in
-- `summary` for every project that had a delivery accepted — that IS the bug,
-- and the owner's original words are already gone, so there is nothing to
-- preserve by leaving it there. A project with a brief row of its own is
-- exactly the set the writer touched; for those, and only where `narrative` is
-- still empty, the text is relocated to the column it belongs in and `summary`
-- is cleared back to "no description yet". A project nobody ever accepted a
-- delivery under is not touched at all, so a hand-typed description survives.
UPDATE projects p
SET narrative = p.summary,
    summary   = ''
WHERE p.narrative = ''
  AND p.summary  <> ''
  AND EXISTS (
    SELECT 1 FROM briefs b
    WHERE b.subject_kind = 'project' AND b.subject_id = p.id
  );
