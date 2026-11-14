DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM coresearch_blob_cleanup) THEN
    RAISE EXCEPTION 'pending Coresearch blob cleanup must complete before rollback';
  END IF;
END $$;
DROP FUNCTION public.coresearch_cleanup_blob_referenced(text);
DROP FUNCTION public.coresearch_blob_cleanup_workspaces();
DROP FUNCTION public.coresearch_enqueue_project_blobs(uuid);
DROP TABLE coresearch_blob_cleanup;
