CREATE TABLE routing_policy (
  scope TEXT PRIMARY KEY,
  engine TEXT NOT NULL,
  confidence DOUBLE PRECISION,
  margin DOUBLE PRECISION,
  eval_run_a TEXT,
  eval_run_b TEXT,
  note TEXT,
  updated_at TIMESTAMPTZ DEFAULT now()
);
