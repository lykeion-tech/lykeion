ALTER TABLE agent_run
 DROP COLUMN cancel_reason,
 DROP COLUMN machine_name,
 DROP COLUMN machine_owner_id,
 DROP CONSTRAINT agent_run_failure_reason_check,
 ADD CONSTRAINT agent_run_failure_reason_check CHECK (failure_reason IN ('runtime_offline', 'timeout', 'provider_network', 'context_exhausted', 'agent_error'));
ALTER TABLE card DROP COLUMN machine;
DROP TABLE runtime_share;
ALTER TABLE runtime DROP COLUMN visibility;
