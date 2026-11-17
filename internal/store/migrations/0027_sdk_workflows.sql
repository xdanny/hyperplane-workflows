CREATE TABLE sdk_workflows (
    id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name        TEXT NOT NULL CHECK (btrim(name) <> ''),
    kind        TEXT NOT NULL CHECK (kind IN ('task', 'app-ideator')),
    instruction TEXT NOT NULL CHECK (btrim(instruction) <> ''),
    target_root TEXT NOT NULL DEFAULT '',
    check_argv  TEXT[] NOT NULL DEFAULT '{}',
    project     TEXT NOT NULL DEFAULT '',
    auto_repair BOOLEAN NOT NULL DEFAULT false,
    revision    BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind = 'task' AND btrim(target_root) <> '' AND cardinality(check_argv) > 0
            AND btrim(check_argv[1]) <> '' AND array_position(check_argv, NULL) IS NULL)
        OR (kind = 'app-ideator' AND target_root = '' AND cardinality(check_argv) = 0))
);
