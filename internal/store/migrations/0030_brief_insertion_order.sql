-- Brief recency follows database-assigned insertion order, not a clock or ID.
-- Existing rows receive ordinals during this change; historical ordering is not
-- reconstructed. Identity allocation may have gaps and is not commit ordering.
ALTER TABLE briefs
    ADD COLUMN ordinal BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE;

DROP INDEX idx_briefs_subject;
CREATE INDEX idx_briefs_subject
    ON briefs (subject_kind, subject_id, ordinal DESC);

DROP INDEX idx_briefs_project;
CREATE INDEX idx_briefs_project
    ON briefs (project, subject_kind, subject_id, ordinal DESC);
