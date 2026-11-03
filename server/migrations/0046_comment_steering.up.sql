ALTER TABLE comment
  ADD COLUMN reply_run_id uuid REFERENCES agent_run(id) ON DELETE SET NULL,
  ADD COLUMN steer_attempt integer;
