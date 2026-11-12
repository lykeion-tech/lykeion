-- Execute as a single statement: atomic in autocommit or a caller transaction.
DO $down$
BEGIN
 IF pg_catalog.current_setting('transaction_isolation') <> 'read committed' THEN
   RAISE EXCEPTION 'cell generation rollback requires READ COMMITTED';
 END IF;
 EXECUTE 'DROP TABLE public.coresearch_cell_generation_capability';
 IF EXISTS (SELECT 1 FROM public.coresearch_cell WHERE outputs_blob ~ '/coresearch/cells/[0-9a-f-]{36}/[0-9a-f-]{36}\.json$') OR EXISTS (SELECT 1 FROM public.coresearch_blob_cleanup WHERE blob_key ~ '/coresearch/cells/[0-9a-f-]{36}/[0-9a-f-]{36}\.json$') OR EXISTS (SELECT 1 FROM public.coresearch_file_version WHERE blob_key ~ '/coresearch/cells/[0-9a-f-]{36}/[0-9a-f-]{36}\.json$') OR EXISTS (SELECT 1 FROM public.coresearch_build WHERE pdf_blob ~ '/coresearch/cells/[0-9a-f-]{36}/[0-9a-f-]{36}\.json$' OR log_blob ~ '/coresearch/cells/[0-9a-f-]{36}/[0-9a-f-]{36}\.json$' OR synctex_blob ~ '/coresearch/cells/[0-9a-f-]{36}/[0-9a-f-]{36}\.json$') THEN RAISE EXCEPTION 'generation cell blobs must be removed before rollback'; END IF;
 EXECUTE 'DROP FUNCTION public.coresearch_cell_candidate_referenced(uuid,uuid)';
 EXECUTE $function$CREATE OR REPLACE FUNCTION public.coresearch_enqueue_project_blobs(project uuid) RETURNS SETOF text
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
END $$;$function$;
END
$down$;
