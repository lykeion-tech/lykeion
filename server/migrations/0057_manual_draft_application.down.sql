DO $application_rollback$
BEGIN
 LOCK TABLE public.lab_chat IN ACCESS EXCLUSIVE MODE;
 IF EXISTS (SELECT 1 FROM public.lab_chat WHERE applied_message_id IS NOT NULL) THEN
  RAISE EXCEPTION USING ERRCODE='55000',
   MESSAGE='Cannot roll back migration 0057 while applied drafting proposal markers exist.',
   HINT='Keep migration 0057 applied; removing application state would replay proposals over saved edits.';
 END IF;
 ALTER TABLE public.lab_chat DROP CONSTRAINT lab_chat_applied_purpose, DROP COLUMN applied_message_id;
END
$application_rollback$;
