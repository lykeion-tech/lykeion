-- Preserve display identities even when an agent or Team is renamed or removed.
CREATE TABLE study_run_worker (
  workspace_id uuid NOT NULL,
  run_id uuid PRIMARY KEY,
  agent_id uuid,
  agent_name text,
  team_id uuid,
  team_name text,
  FOREIGN KEY (workspace_id, run_id) REFERENCES agent_run(workspace_id,id) ON DELETE CASCADE
);
ALTER TABLE study_run_worker ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON study_run_worker USING (workspace_id=app_workspace());
GRANT SELECT, INSERT ON study_run_worker TO lykeion_app;
CREATE FUNCTION snapshot_study_run_worker() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO study_run_worker(workspace_id,run_id,agent_id,agent_name,team_id,team_name)
  SELECT NEW.workspace_id, NEW.id, NEW.agent_id, a.name, p.id, p.name
  FROM (VALUES (1)) seed(n)
  LEFT JOIN agent a ON a.workspace_id=NEW.workspace_id AND a.id=NEW.agent_id
  LEFT JOIN card_team t ON NEW.kind='role' AND t.workspace_id=NEW.workspace_id AND t.card_id=NEW.card_id AND t.board_column=NEW.board_column
  LEFT JOIN field_pack p ON p.workspace_id=t.workspace_id AND p.id=t.pack_id;
  RETURN NEW;
END $$;
CREATE TRIGGER study_run_worker_at_enqueue AFTER INSERT ON agent_run
FOR EACH ROW WHEN (NEW.card_id IS NOT NULL AND NEW.kind IN ('role','rerun','report')) EXECUTE FUNCTION snapshot_study_run_worker();
CREATE INDEX study_run_stage_round ON agent_run(workspace_id,card_id,board_column,round,created_at,id)
  WHERE card_id IS NOT NULL;
