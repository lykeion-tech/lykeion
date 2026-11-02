ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
  ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','context_exhausted','agent_error','runtime_access_denied','coresearch_access_revoked',
    'sign_in','cli_missing','cli_too_old','door','cli_broken','sandbox','fence_breach','hand_in_invalid'));

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
  ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'released', 'autoresearch_card', 'autoresearch_report', 'middle_score', 'blocked',
   'no_tool', 'scoring_unavailable', 'done', 'waiting_for_machine', 'needs_decision', 'open_doubts'));

ALTER TABLE inbox_item ADD COLUMN runtime_id uuid REFERENCES runtime(id) ON DELETE CASCADE, ADD COLUMN cause text;
-- One open machine-wait item per machine and cause, however many runs wait on it.
CREATE UNIQUE INDEX inbox_item_open_machine_wait ON inbox_item (workspace_id, runtime_id, cause)
  WHERE kind = 'waiting_for_machine' AND resolved_at IS NULL;

ALTER TABLE agent_run ADD COLUMN retry_after timestamptz, ADD COLUMN infra_failures int NOT NULL DEFAULT 0;
ALTER TABLE runtime ADD COLUMN unhealthy_reason text;
ALTER TABLE agent_run_usage ADD COLUMN estimated boolean NOT NULL DEFAULT false;
