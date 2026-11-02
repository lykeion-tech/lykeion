ALTER TABLE agent_run_usage DROP COLUMN estimated;
ALTER TABLE runtime DROP COLUMN unhealthy_reason, DROP COLUMN infra_streak;
ALTER TABLE agent_run DROP COLUMN retry_after, DROP COLUMN infra_failures;

DROP INDEX inbox_item_open_machine_wait;
DELETE FROM inbox_item WHERE kind IN ('waiting_for_machine', 'needs_decision', 'open_doubts');
ALTER TABLE inbox_item DROP COLUMN runtime_id, DROP COLUMN cause;
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
  ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done'));

DELETE FROM agent_run WHERE failure_reason IN ('sign_in','cli_missing','cli_too_old','door','cli_broken','sandbox','fence_breach','hand_in_invalid');
ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
  ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','context_exhausted','agent_error','runtime_access_denied','coresearch_access_revoked'));
