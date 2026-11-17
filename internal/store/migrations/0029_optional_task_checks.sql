-- Omitted extra checks are stored as an empty array. Public admission rejects
-- explicitly empty commands; no-check tasks still require a project target.
ALTER TABLE sdk_workflows
    DROP CONSTRAINT sdk_workflows_check,
    ADD CONSTRAINT sdk_workflows_check CHECK (
        (kind = 'task' AND btrim(target_root) <> '' AND nodes = '[]'::jsonb
            AND (cardinality(check_argv) = 0 OR
                (cardinality(check_argv) > 0 AND btrim(check_argv[1]) <> ''
                    AND array_position(check_argv, NULL) IS NULL)))
        OR (kind = 'app-ideator' AND target_root = '' AND cardinality(check_argv) = 0 AND nodes = '[]'::jsonb)
        OR (kind = 'nodes' AND target_root = '' AND cardinality(check_argv) = 0
            AND jsonb_typeof(nodes) = 'array' AND jsonb_array_length(nodes) BETWEEN 1 AND 20)
    );
