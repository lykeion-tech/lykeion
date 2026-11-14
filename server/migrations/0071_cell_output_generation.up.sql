CREATE TABLE public.coresearch_cell_generation_capability (enabled boolean PRIMARY KEY CHECK (enabled));
INSERT INTO public.coresearch_cell_generation_capability VALUES (true);
REVOKE ALL ON public.coresearch_cell_generation_capability FROM PUBLIC;
GRANT SELECT ON public.coresearch_cell_generation_capability TO lykeion_app;

CREATE OR REPLACE FUNCTION public.coresearch_enqueue_project_blobs(project uuid) RETURNS SETOF text
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
    IF key !~ ('^' || public.app_workspace()::text || '/coresearch/([0-9a-f]{64}|cells/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?\.json)$') THEN
      RAISE EXCEPTION 'invalid coresearch blob key' USING ERRCODE='22023';
    END IF;
    INSERT INTO public.coresearch_blob_cleanup(workspace_id,blob_key)
      VALUES(public.app_workspace(),key) ON CONFLICT DO NOTHING;
    RETURN NEXT key;
  END LOOP;
END $$;
REVOKE ALL ON FUNCTION public.coresearch_enqueue_project_blobs(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_enqueue_project_blobs(uuid) TO lykeion_app;


CREATE FUNCTION public.coresearch_cell_candidate_referenced(cell_id uuid, attempt_id uuid) RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE k text;
BEGIN
  IF cell_id IS NULL OR attempt_id IS NULL OR public.app_workspace() IS NULL OR public.app_user() IS NULL THEN RAISE EXCEPTION 'missing scope' USING ERRCODE='42501'; END IF;
  PERFORM 1 FROM public.coresearch_cell c JOIN public.coresearch_chat ch ON ch.workspace_id=c.workspace_id AND ch.id=c.chat_id
    JOIN public.member m ON m.workspace_id=ch.workspace_id AND m.user_id=public.app_user()
    JOIN public.coresearch_member pm ON pm.workspace_id=ch.workspace_id AND pm.project_id=ch.project_id AND pm.user_id=public.app_user()
    WHERE c.workspace_id=public.app_workspace() AND c.id=cell_id AND ch.owner_id=public.app_user() FOR UPDATE OF c;
  IF NOT FOUND THEN RAISE EXCEPTION 'cell owner required' USING ERRCODE='42501'; END IF;
  k := public.app_workspace()::text || '/coresearch/cells/' || cell_id::text || '/' || attempt_id::text || '.json';
  RETURN EXISTS(SELECT 1 FROM public.coresearch_cell c WHERE c.workspace_id=public.app_workspace() AND c.outputs_blob=k)
    OR EXISTS(SELECT 1 FROM public.coresearch_file_version v WHERE v.workspace_id=public.app_workspace() AND v.blob_key=k)
    OR EXISTS(SELECT 1 FROM public.coresearch_build b WHERE b.workspace_id=public.app_workspace() AND (b.pdf_blob=k OR b.log_blob=k OR b.synctex_blob=k));
END $$;
REVOKE ALL ON FUNCTION public.coresearch_cell_candidate_referenced(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_cell_candidate_referenced(uuid,uuid) TO lykeion_app;
