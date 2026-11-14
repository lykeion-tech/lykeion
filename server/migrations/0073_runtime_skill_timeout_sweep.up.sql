-- Discover only workspace IDs for bounded maintenance; tenant rows are updated
-- under the normal workspace policy and event locks by the sweep transaction.
-- This transactional index build blocks writes to runtime_skill_request until it
-- completes. Schedule the migration during a suitable maintenance window.
CREATE INDEX runtime_skill_request_expiry_sweep
ON public.runtime_skill_request (workspace_id, expires_at, id)
WHERE status IN ('pending', 'running');

CREATE FUNCTION public.runtime_skill_timeout_candidates(after_workspace uuid) RETURNS TABLE(workspace_id uuid, eligible boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
  IF public.app_user() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid
     OR public.app_workspace() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid THEN
    RAISE EXCEPTION 'maintenance identity required' USING ERRCODE = '42501';
  END IF;
  IF after_workspace IS NULL THEN
    RAISE EXCEPTION 'invalid maintenance cursor' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
    SELECT w.id, EXISTS (
      SELECT 1 FROM public.runtime_skill_request q
      WHERE q.workspace_id = w.id AND q.status IN ('pending', 'running')
        AND q.expires_at <= pg_catalog.statement_timestamp()
    )
    FROM (SELECT id FROM public.workspace WHERE id > after_workspace ORDER BY id LIMIT 64) w
    ORDER BY w.id;
END $$;

REVOKE ALL ON FUNCTION public.runtime_skill_timeout_candidates(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.runtime_skill_timeout_candidates(uuid) TO lykeion_app;
