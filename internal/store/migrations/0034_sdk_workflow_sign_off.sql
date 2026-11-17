-- Sign-off policy per saved workflow. "required" is what every existing
-- workflow already did, so the default preserves today's behavior exactly.
-- "optional" keeps a finished run decidable without queuing it for a person;
-- "auto_when_checks_pass" lets the server record an automatic acceptance when
-- every declared check of a passed run passed. The policy each run was launched
-- under is captured on the run, so editing this column never restates an old
-- run's terms.
ALTER TABLE sdk_workflows
    ADD COLUMN sign_off TEXT NOT NULL DEFAULT 'required'
        CHECK (sign_off IN ('required', 'optional', 'auto_when_checks_pass'));
