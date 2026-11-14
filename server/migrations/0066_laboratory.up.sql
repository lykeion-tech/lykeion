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

-- Merges every person's private copy of a laboratory member into the shared
-- agent: rows that make up history are pointed at it, and rows that belong
-- to an agent itself (cascading foreign keys) are dropped, the shared Team
-- row excepted. Runs as the owner so rows hidden from the caller by
-- per-person policies move too. Returns the agents whose own rows beyond
-- their Team membership were dropped.
CREATE FUNCTION laboratory_merge(keep uuid) RETURNS uuid[]
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  ws uuid := public.app_workspace();
  copies uuid[];
  everyone uuid[];
  ref record;
  hit uuid[];
  dropped uuid[] := '{}';
BEGIN
  SELECT array_agg(DISTINCT c.agent_id) INTO copies
  FROM public.team_agent s
  JOIN public.agent a ON a.workspace_id = s.workspace_id AND a.id = s.agent_id AND a.laboratory <> ''
  JOIN public.team_agent c ON c.workspace_id = s.workspace_id AND c.pack_id = s.pack_id AND c.member = s.member
    AND c.user_id IS NOT NULL AND c.agent_id <> keep
  WHERE s.workspace_id = ws AND s.agent_id = keep AND s.user_id IS NULL;
  IF NOT EXISTS (SELECT 1 FROM public.team_agent s JOIN public.agent a ON a.workspace_id = s.workspace_id AND a.id = s.agent_id
      WHERE s.workspace_id = ws AND s.agent_id = keep AND s.user_id IS NULL AND a.laboratory <> '') THEN
    RAISE EXCEPTION 'not a laboratory agent' USING ERRCODE = '22023';
  END IF;
  copies := coalesce(copies, '{}');
  everyone := copies || keep;
  FOR ref IN
    SELECT c.conrelid::regclass::text AS tbl, a.attname::text AS col, c.confdeltype = 'c' AS owned, '' AS cond
    FROM pg_constraint c CROSS JOIN LATERAL unnest(c.conkey, c.confkey) k(col, refcol)
    JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.col
    JOIN pg_attribute r ON r.attrelid = c.confrelid AND r.attnum = k.refcol
    WHERE c.contype = 'f' AND c.confrelid = 'public.agent'::regclass AND r.attname = 'id'
    UNION ALL VALUES ('card', 'assignee_id', false, ' AND assignee_type IN (''agent'', ''team'')'),
      ('autoresearch', 'assignee_id', false, ' AND assignee_type IN (''agent'', ''team'')'),
      ('research', 'lead_id', false, ' AND lead_type = ''agent''')
  LOOP
    IF ref.owned THEN
      EXECUTE format('WITH d AS (DELETE FROM %s WHERE workspace_id = $1 AND %I = ANY($2)%s RETURNING %I) SELECT array_agg(%I) FROM d',
        ref.tbl, ref.col, CASE WHEN ref.tbl = 'team_agent' THEN ' AND user_id IS NOT NULL' ELSE '' END, ref.col, ref.col)
        INTO hit USING ws, everyone;
      IF ref.tbl <> 'team_agent' THEN
        dropped := dropped || coalesce(hit, '{}');
      END IF;
    ELSIF cardinality(copies) > 0 THEN
      EXECUTE format('UPDATE %s SET %I = $3 WHERE workspace_id = $1 AND %I = ANY($2)%s', ref.tbl, ref.col, ref.col, ref.cond)
        USING ws, copies, keep;
    END IF;
  END LOOP;
  RETURN ARRAY(SELECT DISTINCT unnest(dropped));
END $$;
REVOKE ALL ON FUNCTION laboratory_merge(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION laboratory_merge(uuid) TO lykeion_app;
