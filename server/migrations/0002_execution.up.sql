CREATE TABLE runtime (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  owner_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  daemon_id text NOT NULL,
  harness text NOT NULL CHECK (harness IN ('claude', 'codex')),
  device_name text NOT NULL DEFAULT '',
  custom_name text CHECK (char_length(custom_name) BETWEEN 1 AND 64),
  status text NOT NULL DEFAULT 'online' CHECK (status IN ('online', 'offline')),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  max_concurrent int NOT NULL DEFAULT 2 CHECK (max_concurrent BETWEEN 1 AND 32),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, daemon_id, harness),
  UNIQUE (id, workspace_id)
);

CREATE TABLE agent_run (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  responsible_user_id uuid NOT NULL REFERENCES "user",
  board_column text NOT NULL,
  round int NOT NULL,
  role text NOT NULL,
  harness text NOT NULL CHECK (harness IN ('claude', 'codex')),
  prompt text NOT NULL,
  skill text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'dispatched', 'running', 'completed', 'failed', 'cancelled')),
  queue_reason text CHECK (queue_reason IN ('runtime_offline', 'budget')),
  runtime_id uuid,
  prepare_lease_expires_at timestamptz,
  dispatched_at timestamptz,
  started_at timestamptz,
  completed_at timestamptz,
  attempt int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 2,
  session_id text NOT NULL DEFAULT '',
  continuity_scope text NOT NULL DEFAULT '',
  run_private boolean NOT NULL DEFAULT false,
  profile_mode text NOT NULL DEFAULT '',
  policy_digest text NOT NULL DEFAULT '',
  model text NOT NULL DEFAULT '',
  error text NOT NULL DEFAULT '',
  failure_reason text CHECK (failure_reason IN
    ('runtime_offline', 'timeout', 'provider_network', 'context_exhausted', 'agent_error')),
  cost_usd double precision NOT NULL DEFAULT 0,
  late_usage_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, workspace_id),
  UNIQUE (id, workspace_id, card_id, role),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (runtime_id, workspace_id) REFERENCES runtime (id, workspace_id) ON DELETE SET NULL (runtime_id)
);
CREATE INDEX agent_run_queued ON agent_run (workspace_id, created_at) WHERE status = 'queued';

CREATE TABLE agent_run_usage (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid NOT NULL,
  attempt int NOT NULL CHECK (attempt > 0),
  model text NOT NULL,
  input_tokens bigint NOT NULL CHECK (input_tokens >= 0),
  output_tokens bigint NOT NULL CHECK (output_tokens >= 0),
  cache_read_tokens bigint NOT NULL CHECK (cache_read_tokens >= 0),
  cache_write_tokens bigint NOT NULL CHECK (cache_write_tokens >= 0),
  cost_usd double precision NOT NULL CHECK (cost_usd >= 0),
  cost_reported boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (agent_run_id, attempt, model),
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);
CREATE INDEX agent_run_usage_agent_run ON agent_run_usage (agent_run_id);

CREATE TABLE agent_run_message (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid NOT NULL,
  attempt int NOT NULL CHECK (attempt > 0),
  seq int NOT NULL,
  type text NOT NULL CHECK (type IN ('text', 'thinking', 'tool_use', 'tool_result', 'status', 'error', 'log')),
  tool text NOT NULL DEFAULT '',
  content jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (agent_run_id, attempt, seq),
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);

CREATE TABLE agent_run_token (
  token_hash text PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  agent_run_id uuid NOT NULL,
  card_id uuid NOT NULL,
  role text NOT NULL,
  attempt int NOT NULL CHECK (attempt > 0),
  FOREIGN KEY (agent_run_id, workspace_id, card_id, role)
    REFERENCES agent_run (id, workspace_id, card_id, role) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL
);

CREATE TABLE record (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid NOT NULL,
  board_column text NOT NULL,
  round int NOT NULL,
  kind text NOT NULL,
  role text NOT NULL,
  key text NOT NULL DEFAULT '',
  body jsonb NOT NULL DEFAULT '{}',
  blob_key text NOT NULL DEFAULT '',
  agent_run_id uuid,
  superseded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE SET NULL (agent_run_id)
);
CREATE INDEX record_live ON record (card_id, board_column, kind) WHERE superseded_at IS NULL;

ALTER TABLE runtime ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON runtime USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE agent_run ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE agent_run_usage ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run_usage USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE agent_run_message ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run_message USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE agent_run_token ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON agent_run_token USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE record ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON record USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());

CREATE FUNCTION agent_run_token_lookup(h text)
RETURNS TABLE (agent_run_id uuid, workspace_id uuid, card_id uuid, role text, board_column text, round int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT t.id, t.workspace_id, t.card_id, k.role, t.board_column, t.round
  FROM agent_run_token k JOIN agent_run t ON t.id = k.agent_run_id
  WHERE k.token_hash = h AND k.expires_at > now() AND t.status = 'running'
    AND k.attempt = t.attempt AND k.workspace_id = t.workspace_id
    AND k.card_id = t.card_id AND k.role = t.role
$$;

CREATE FUNCTION sweep_runtimes(stale interval) RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  WITH x AS (
    UPDATE runtime SET status = 'offline'
    WHERE status = 'online' AND last_seen_at < now() - stale
    RETURNING 1
  ) SELECT count(*)::int FROM x
$$;

CREATE FUNCTION sweep_agent_runs()
RETURNS TABLE (agent_run_id uuid, workspace_id uuid, card_id uuid, status text)
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  WITH swept AS (
    UPDATE agent_run t SET
      status = CASE WHEN t.attempt >= t.max_attempts THEN 'failed' ELSE 'queued' END,
      error = CASE WHEN t.attempt >= t.max_attempts THEN 'lost: the lease expired or the runtime went offline' ELSE t.error END,
      failure_reason = CASE WHEN t.status = 'dispatched' THEN 'timeout' ELSE 'runtime_offline' END,
      completed_at = CASE WHEN t.attempt >= t.max_attempts THEN now() ELSE NULL END,
      runtime_id = NULL,
      prepare_lease_expires_at = NULL,
      started_at = NULL
    WHERE (t.status = 'dispatched' AND t.prepare_lease_expires_at < now())
       OR (t.status = 'running' AND (t.runtime_id IS NULL OR EXISTS (
            SELECT 1 FROM runtime r WHERE r.id = t.runtime_id AND r.status = 'offline')))
    RETURNING t.id, t.workspace_id, t.card_id, t.status
  ), dropped AS (
    DELETE FROM agent_run_token k USING swept s WHERE k.agent_run_id = s.id
  )
  SELECT id, workspace_id, card_id, status FROM swept
$$;

REVOKE ALL ON FUNCTION agent_run_token_lookup(text), sweep_runtimes(interval), sweep_agent_runs() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_run_token_lookup(text), sweep_runtimes(interval), sweep_agent_runs() TO lykeion_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON runtime, agent_run, agent_run_usage, agent_run_message, agent_run_token, record TO lykeion_app;
