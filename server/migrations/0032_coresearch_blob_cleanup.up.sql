CREATE TABLE coresearch_blob_cleanup (
  workspace_id uuid NOT NULL,
  blob_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, blob_key)
);
ALTER TABLE coresearch_blob_cleanup ENABLE ROW LEVEL SECURITY;
CREATE POLICY maintenance ON coresearch_blob_cleanup
  USING (workspace_id = app_workspace() AND app_user() = '00000000-0000-0000-0000-000000000000'::uuid)
  WITH CHECK (workspace_id = app_workspace() AND app_user() = '00000000-0000-0000-0000-000000000000'::uuid);
GRANT SELECT, UPDATE, DELETE ON coresearch_blob_cleanup TO lykeion_app;

CREATE FUNCTION public.coresearch_enqueue_project_blobs(project uuid) RETURNS SETOF text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE key text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM public.coresearch_project p JOIN public.coresearch_conversation c
      ON c.workspace_id=p.workspace_id AND c.project_id=p.id
      JOIN public.member m ON m.workspace_id=p.workspace_id AND m.user_id=public.app_user()
      WHERE p.workspace_id=public.app_workspace() AND p.id=project
        AND p.created_by=public.app_user() AND c.owner_id=public.app_user()) THEN
    RAISE EXCEPTION 'project owner required' USING ERRCODE='42501';
  END IF;
  FOR key IN
    SELECT candidate FROM (
      SELECT v.blob_key AS candidate FROM public.coresearch_file_version v
        JOIN public.coresearch_file f ON f.workspace_id=v.workspace_id AND f.id=v.file_id
        WHERE f.workspace_id=public.app_workspace() AND f.project_id=project
      UNION SELECT b.pdf_blob FROM public.coresearch_build b WHERE b.workspace_id=public.app_workspace() AND b.project_id=project AND b.pdf_blob IS NOT NULL
      UNION SELECT b.log_blob FROM public.coresearch_build b WHERE b.workspace_id=public.app_workspace() AND b.project_id=project AND b.log_blob IS NOT NULL
      UNION SELECT b.synctex_blob FROM public.coresearch_build b WHERE b.workspace_id=public.app_workspace() AND b.project_id=project AND b.synctex_blob IS NOT NULL
      UNION SELECT c.outputs_blob FROM public.coresearch_cell c JOIN public.coresearch_chat ch
        ON ch.workspace_id=c.workspace_id AND ch.id=c.chat_id
        WHERE ch.workspace_id=public.app_workspace() AND ch.project_id=project AND c.outputs_blob IS NOT NULL
    ) blobs ORDER BY candidate
  LOOP
    IF key !~ ('^' || public.app_workspace()::text || '/coresearch/([0-9a-f]{64}|cells/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json)$') THEN
      RAISE EXCEPTION 'invalid coresearch blob key' USING ERRCODE='22023';
    END IF;
    INSERT INTO public.coresearch_blob_cleanup(workspace_id,blob_key)
      VALUES(public.app_workspace(),key) ON CONFLICT DO NOTHING;
    RETURN NEXT key;
  END LOOP;
END $$;
REVOKE ALL ON FUNCTION public.coresearch_enqueue_project_blobs(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_enqueue_project_blobs(uuid) TO lykeion_app;

CREATE FUNCTION public.coresearch_blob_cleanup_workspaces() RETURNS SETOF uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
BEGIN
  IF public.app_user() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid THEN
    RAISE EXCEPTION 'maintenance identity required' USING ERRCODE='42501';
  END IF;
  RETURN QUERY SELECT DISTINCT q.workspace_id FROM public.coresearch_blob_cleanup q ORDER BY q.workspace_id;
END $$;
REVOKE ALL ON FUNCTION public.coresearch_blob_cleanup_workspaces() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_blob_cleanup_workspaces() TO lykeion_app;

CREATE FUNCTION public.coresearch_cleanup_blob_referenced(key text) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public AS $$
BEGIN
  IF public.app_user() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid OR
     NOT EXISTS (SELECT 1 FROM public.coresearch_blob_cleanup q WHERE q.workspace_id=public.app_workspace() AND q.blob_key=key) THEN
    RAISE EXCEPTION 'maintenance identity and queued key required' USING ERRCODE='42501';
  END IF;
  RETURN EXISTS(SELECT 1 FROM public.coresearch_file_version v WHERE v.workspace_id=public.app_workspace() AND v.blob_key=key)
    OR EXISTS(SELECT 1 FROM public.coresearch_build b WHERE b.workspace_id=public.app_workspace() AND (b.pdf_blob=key OR b.log_blob=key OR b.synctex_blob=key))
    OR EXISTS(SELECT 1 FROM public.coresearch_cell c WHERE c.workspace_id=public.app_workspace() AND c.outputs_blob=key);
END $$;
REVOKE ALL ON FUNCTION public.coresearch_cleanup_blob_referenced(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_cleanup_blob_referenced(text) TO lykeion_app;
