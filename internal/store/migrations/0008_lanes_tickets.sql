CREATE TABLE lanes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active', -- active|done|archived
  target_root TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tickets (
  id TEXT PRIMARY KEY,
  lane_id TEXT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'todo', -- todo|in_progress|review|done|blocked
  priority INT NOT NULL DEFAULT 0,
  assignee TEXT NOT NULL DEFAULT '',
  deps JSONB NOT NULL DEFAULT '[]',
  output TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX tickets_lane_status ON tickets(lane_id, status);
CREATE INDEX tickets_lane_priority ON tickets(lane_id, priority DESC, created_at ASC);
