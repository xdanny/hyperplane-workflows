-- Preserve the current attempt's identity even if the run is later deleted.
ALTER TABLE tickets ADD COLUMN sdk_run_id text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX tickets_sdk_run_id ON tickets (sdk_run_id) WHERE sdk_run_id <> '';
