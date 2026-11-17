-- Who recorded a decision. Hyperplane is local-first with no auth, so a person's
-- decision stays 'local' — the one keyboard we can honestly name. An acceptance
-- the server recorded from a workflow's sign-off policy is 'hyperplane:auto',
-- which every surface labels as automatic and skill extraction refuses to treat
-- as a person's acceptance.
ALTER TABLE decisions
    ADD COLUMN decided_by TEXT NOT NULL DEFAULT 'local'
        CHECK (btrim(decided_by) <> '' AND length(decided_by) <= 64);
