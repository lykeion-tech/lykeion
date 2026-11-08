ALTER TABLE agent
  ADD COLUMN stage text NOT NULL DEFAULT '' CHECK (stage IN ('', 'grounding', 'hypotheses', 'experimentation', 'validation')),
  ADD COLUMN laboratory text NOT NULL DEFAULT '' CHECK (laboratory IN ('', 'built-in')),
  ADD CONSTRAINT agent_laboratory_shape CHECK (laboratory = '' OR (stage <> '' AND visibility = 'workspace'));

ALTER TABLE workspace ADD COLUMN laboratory text NOT NULL DEFAULT 'built-in' CHECK (laboratory IN ('built-in'));

-- A NULL user_id is the workspace's shared copy of a Team member.
ALTER TABLE team_agent DROP CONSTRAINT team_agent_pkey;
ALTER TABLE team_agent ALTER COLUMN user_id DROP NOT NULL;
CREATE UNIQUE INDEX team_agent_personal ON team_agent (pack_id, user_id, member) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX team_agent_shared ON team_agent (pack_id, member) WHERE user_id IS NULL;

UPDATE agent SET stage = p.stage
FROM team_agent t JOIN field_pack p ON p.id = t.pack_id
WHERE agent.id = t.agent_id AND p.source = 'shipped';
