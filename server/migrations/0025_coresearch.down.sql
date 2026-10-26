DROP FUNCTION public.coresearch_settle(uuid);
DROP FUNCTION public.coresearch_blob_referenced(text);
DROP FUNCTION public.coresearch_create_project(text);
DELETE FROM agent_run WHERE kind = 'coresearch';
ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','context_exhausted','agent_error','runtime_access_denied'));
ALTER TABLE agent_run
  DROP CONSTRAINT agent_run_kind_shape,
  DROP CONSTRAINT agent_run_kind_check,
  DROP COLUMN coresearch_chat_id,
  ADD CONSTRAINT agent_run_kind_check CHECK (kind IN ('role', 'autoresearch')),
  ADD CONSTRAINT agent_run_kind_shape CHECK (
    (kind = 'role' AND card_id IS NOT NULL)
    OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL));
DROP TABLE coresearch_setup, coresearch_run, coresearch_cell, coresearch_message, coresearch_chat,
  coresearch_file_version, coresearch_file, coresearch_member, coresearch_project;
DROP FUNCTION coresearch_file_project(uuid), coresearch_chat_project(uuid), coresearch_can_see(uuid);
