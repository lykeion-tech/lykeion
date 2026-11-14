DROP FUNCTION laboratory_merge(uuid);
DELETE FROM team_agent WHERE user_id IS NULL;
DROP INDEX team_agent_shared;
DROP INDEX team_agent_personal;
ALTER TABLE team_agent ADD PRIMARY KEY (pack_id, user_id, member);
ALTER TABLE team_agent ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE workspace DROP COLUMN laboratory;
ALTER TABLE agent DROP CONSTRAINT agent_laboratory_shape, DROP COLUMN laboratory, DROP COLUMN stage;
