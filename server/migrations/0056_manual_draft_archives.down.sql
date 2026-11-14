DO $archive_rollback$
BEGIN
 LOCK TABLE public.lab_chat IN ACCESS EXCLUSIVE MODE;
 IF EXISTS (SELECT 1 FROM public.lab_chat WHERE archived_at IS NOT NULL) THEN
  RAISE EXCEPTION USING ERRCODE='55000',
   MESSAGE='Cannot roll back migration 0056 while archived drafting conversations exist.',
   HINT='Keep migration 0056 applied; removing archive state would make read-only conversations editable again.';
 END IF;
 ALTER TABLE public.lab_chat DROP CONSTRAINT lab_chat_archive_purpose, DROP COLUMN archived_at;
END
$archive_rollback$;
