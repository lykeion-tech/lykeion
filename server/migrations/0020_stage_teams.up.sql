-- Studies are worked by Teams only: a person's study assigned to a member or an
-- agent, and anything left in Backlog, goes. Autoresearch studies stay.
DELETE FROM card
WHERE origin = '' AND (assignee_type IN ('member', 'agent') OR board_column = 'backlog');
DELETE FROM card WHERE board_column = 'backlog';

DO $$
BEGIN
-- One row per Team name: every reference to an older version moves to the newest.
CREATE TEMP TABLE pack_newest ON COMMIT DROP AS
SELECT o.id AS old_id, n.id AS new_id
FROM field_pack o
JOIN (SELECT DISTINCT ON (workspace_id, name) workspace_id, name, id
      FROM field_pack ORDER BY workspace_id, name, created_at DESC, id DESC) n
  ON n.workspace_id = o.workspace_id AND n.name = o.name AND n.id <> o.id;

UPDATE card_team SET pack_id = m.new_id FROM pack_newest m WHERE pack_id = m.old_id;
UPDATE team_rating SET pack_id = m.new_id FROM pack_newest m WHERE pack_id = m.old_id;
UPDATE autoresearch_team SET pack_id = m.new_id FROM pack_newest m WHERE pack_id = m.old_id;
UPDATE card SET pack_id = m.new_id FROM pack_newest m WHERE pack_id = m.old_id;

INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id)
SELECT t.workspace_id, m.new_id, t.user_id, t.member, t.agent_id
FROM team_agent t JOIN pack_newest m ON t.pack_id = m.old_id
ON CONFLICT DO NOTHING;
INSERT INTO field_pack_approval (workspace_id, pack_id, approved_by, approved_at)
SELECT a.workspace_id, m.new_id, a.approved_by, a.approved_at
FROM field_pack_approval a JOIN pack_newest m ON a.pack_id = m.old_id
ON CONFLICT DO NOTHING;

DELETE FROM field_pack WHERE id IN (SELECT old_id FROM pack_newest);

END $$;

ALTER TABLE field_pack DROP CONSTRAINT field_pack_workspace_id_name_version_key;
ALTER TABLE field_pack ADD CONSTRAINT field_pack_workspace_id_name_key UNIQUE (workspace_id, name);

-- Every workspace id, for startup work that installs shipped Teams in each one.
CREATE FUNCTION all_workspace_ids() RETURNS TABLE (id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT w.id FROM workspace w
$$;
REVOKE ALL ON FUNCTION all_workspace_ids() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION all_workspace_ids() TO lykeion_app;

ALTER TABLE card ALTER COLUMN board_column DROP DEFAULT;
ALTER TABLE card ADD CONSTRAINT card_board_column_check
  CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting', 'done'));
