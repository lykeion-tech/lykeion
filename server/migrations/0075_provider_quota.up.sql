ALTER TABLE agent_run DROP CONSTRAINT agent_run_failure_reason_check,
  ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline','timeout','provider_network','provider_quota','context_exhausted','agent_error','runtime_access_denied','coresearch_access_revoked',
    'sign_in','cli_missing','cli_too_old','door','cli_broken','sandbox','fence_breach','hand_in_invalid','lab_context_changed'));
