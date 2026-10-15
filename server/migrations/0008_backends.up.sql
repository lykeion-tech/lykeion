-- The harnesses a runtime may offer and an agent run may ask for: the names in
-- agent.Harnesses.
ALTER TABLE runtime DROP CONSTRAINT runtime_harness_check,
  ADD CONSTRAINT runtime_harness_check CHECK (harness IN ('claude', 'codex', 'cursor', 'antigravity'));
ALTER TABLE agent_run DROP CONSTRAINT agent_run_harness_check,
  ADD CONSTRAINT agent_run_harness_check CHECK (harness IN ('claude', 'codex', 'cursor', 'antigravity'));
ALTER TABLE agent DROP CONSTRAINT agent_harness_check,
  ADD CONSTRAINT agent_harness_check CHECK (harness IN ('claude', 'codex', 'cursor', 'antigravity'));

-- While a running agent run waits on inputs the server fetches for it: the
-- lease its daemon renews by asking again, the claim of the one server
-- fetching, and each file's outcome until a run pins or logs it.
ALTER TABLE agent_run
  ADD COLUMN input_lease_expires_at timestamptz,
  ADD COLUMN input_fetch_expires_at timestamptz,
  ADD COLUMN input_fetch_id uuid,
  ADD COLUMN input_outcomes jsonb NOT NULL DEFAULT '{}';
