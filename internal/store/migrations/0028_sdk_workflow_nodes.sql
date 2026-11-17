ALTER TABLE sdk_workflows
    ADD COLUMN nodes JSONB NOT NULL DEFAULT '[]'::jsonb,
    DROP CONSTRAINT sdk_workflows_kind_check,
    DROP CONSTRAINT sdk_workflows_check,
    ADD CONSTRAINT sdk_workflows_kind_check CHECK (kind IN ('task', 'app-ideator', 'nodes')),
    ADD CONSTRAINT sdk_workflows_check CHECK (
        (kind = 'task' AND btrim(target_root) <> '' AND cardinality(check_argv) > 0
            AND btrim(check_argv[1]) <> '' AND array_position(check_argv, NULL) IS NULL AND nodes = '[]'::jsonb)
        OR (kind = 'app-ideator' AND target_root = '' AND cardinality(check_argv) = 0 AND nodes = '[]'::jsonb)
        OR (kind = 'nodes' AND target_root = '' AND cardinality(check_argv) = 0
            AND jsonb_typeof(nodes) = 'array' AND jsonb_array_length(nodes) BETWEEN 1 AND 20)
    );
