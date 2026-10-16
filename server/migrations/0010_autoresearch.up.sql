-- Agent runs with no card. A role run belongs to a card and runs on the card
-- owner's runtimes; an autoresearch agent run belongs to an Autoresearch run and runs on
-- the runtimes of the user it acts for, its requester.
ALTER TABLE agent_run ALTER COLUMN card_id DROP NOT NULL;
ALTER TABLE agent_run ADD COLUMN kind text NOT NULL DEFAULT 'role' CHECK (kind IN ('role', 'autoresearch'));
ALTER TABLE agent_run ADD COLUMN requester_id uuid REFERENCES "user";
ALTER TABLE agent_run ADD CONSTRAINT agent_run_kind_shape CHECK (
  (kind = 'role' AND card_id IS NOT NULL)
  OR (kind = 'autoresearch' AND card_id IS NULL AND requester_id IS NOT NULL));

-- What an agent run token may do. A role token acts as its role on its card; an
-- autoresearch token may only post its run's report and open Backlog cards.
ALTER TABLE agent_run_token ALTER COLUMN card_id DROP NOT NULL;
ALTER TABLE agent_run_token ADD COLUMN scopes text[] NOT NULL DEFAULT '{card:role}';
-- The (agent_run_id, workspace_id, card_id, role) key is not checked when card_id
-- is NULL, so a card-less token is tied to its run and workspace by this one.
ALTER TABLE agent_run_token ADD CONSTRAINT agent_run_token_run_fkey
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE;

-- A token is good only for its run's current attempt, workspace, card and role.
-- A card-less token matches a card-less run: NULL is not distinct from NULL.
DROP FUNCTION agent_run_token_lookup(text);
CREATE FUNCTION agent_run_token_lookup(h text)
RETURNS TABLE (agent_run_id uuid, workspace_id uuid, card_id uuid, role text, board_column text, round int, scopes text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT t.id, t.workspace_id, t.card_id, k.role, t.board_column, t.round, k.scopes
  FROM agent_run_token k JOIN agent_run t ON t.id = k.agent_run_id
  WHERE k.token_hash = h AND k.expires_at > now() AND t.status = 'running'
    AND k.attempt = t.attempt AND k.workspace_id = t.workspace_id
    AND k.card_id IS NOT DISTINCT FROM t.card_id AND k.role = t.role
$$;
REVOKE ALL ON FUNCTION agent_run_token_lookup(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_run_token_lookup(text) TO lykeion_app;

-- Where a card came from: '' for a person's card, autoresearch:<id> for a card an
-- Autoresearch opened.
ALTER TABLE card ADD COLUMN origin text NOT NULL DEFAULT '';
CREATE INDEX card_origin ON card (workspace_id, origin, number) WHERE origin <> '';

CREATE TABLE autoresearch (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  owner_id uuid NOT NULL REFERENCES "user",
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
  execution_mode text NOT NULL CHECK (execution_mode IN ('create_card', 'run_only')),
  title_template text NOT NULL DEFAULT '' CHECK (char_length(title_template) <= 200),
  budget_usd double precision NOT NULL DEFAULT 0 CHECK (budget_usd >= 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused')),
  pause_reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Collaborators may edit and trigger an Autoresearch; subscribers get an Inbox
-- item when one of its runs opens a card or posts a report.
CREATE TABLE autoresearch_collaborator (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  autoresearch_id uuid NOT NULL REFERENCES autoresearch ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  PRIMARY KEY (autoresearch_id, user_id)
);
CREATE TABLE autoresearch_subscriber (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  autoresearch_id uuid NOT NULL REFERENCES autoresearch ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  PRIMARY KEY (autoresearch_id, user_id)
);

-- A schedule fires at next_run_at; a webhook is found by the sha256 of its URL
-- token. A signing secret is stored sealed, never in the clear, and only a
-- github webhook can have one.
CREATE TABLE autoresearch_trigger (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  autoresearch_id uuid NOT NULL REFERENCES autoresearch ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('schedule', 'webhook')),
  cron text NOT NULL DEFAULT '' CHECK (char_length(cron) <= 100),
  tz text NOT NULL DEFAULT 'UTC',
  next_run_at timestamptz,
  token_hash text UNIQUE,
  signing_secret_enc bytea,
  provider text NOT NULL DEFAULT 'generic' CHECK (provider IN ('generic', 'github')),
  filters jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(filters) = 'array'),
  enabled boolean NOT NULL DEFAULT true,
  created_by uuid NOT NULL REFERENCES "user",
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'schedule' AND cron <> '' AND next_run_at IS NOT NULL AND token_hash IS NULL)
      OR (kind = 'webhook' AND cron = '' AND token_hash IS NOT NULL)),
  CHECK (signing_secret_enc IS NULL OR provider = 'github')
);
CREATE INDEX autoresearch_trigger_due ON autoresearch_trigger (next_run_at) WHERE kind = 'schedule' AND enabled;

-- One row per firing. The idempotency key makes a firing count once however
-- often it is retried. payload is the webhook envelope; '{}' for other runs.
CREATE TABLE autoresearch_run (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  autoresearch_id uuid NOT NULL REFERENCES autoresearch ON DELETE CASCADE,
  trigger_id uuid REFERENCES autoresearch_trigger ON DELETE SET NULL,
  source text NOT NULL CHECK (source IN ('schedule', 'webhook', 'manual')),
  acting_user_id uuid NOT NULL REFERENCES "user",
  planned_at timestamptz NOT NULL,
  status text NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'completed', 'failed', 'skipped', 'blocked')),
  reason text NOT NULL DEFAULT '',
  payload jsonb NOT NULL DEFAULT '{}',
  card_id uuid,
  agent_run_id uuid,
  report text NOT NULL DEFAULT '',
  idempotency_key text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE SET NULL (card_id),
  FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE SET NULL (agent_run_id)
);
CREATE INDEX autoresearch_run_list ON autoresearch_run (autoresearch_id, created_at DESC);
CREATE INDEX autoresearch_run_open ON autoresearch_run (status) WHERE status IN ('queued', 'running', 'blocked');

-- A webhook request that was accepted, waiting for the delivery worker.
CREATE TABLE webhook_delivery (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  trigger_id uuid NOT NULL REFERENCES autoresearch_trigger ON DELETE CASCADE,
  delivery_key text NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now(),
  envelope jsonb NOT NULL,
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'dispatching', 'dispatched', 'failed')),
  attempts int NOT NULL DEFAULT 0,
  lease_until timestamptz,
  run_id uuid REFERENCES autoresearch_run ON DELETE SET NULL,
  UNIQUE (trigger_id, delivery_key)
);
CREATE INDEX webhook_delivery_open ON webhook_delivery (received_at) WHERE state IN ('pending', 'dispatching');

-- Inbox items about an Autoresearch run need no card: a report has none.
ALTER TABLE inbox_item ALTER COLUMN card_id DROP NOT NULL;
ALTER TABLE inbox_item ADD COLUMN autoresearch_run_id uuid REFERENCES autoresearch_run ON DELETE SET NULL;
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'out_of_budget', 'released', 'publish_request', 'autoresearch_card', 'autoresearch_report'));

ALTER TABLE autoresearch ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON autoresearch USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE autoresearch_collaborator ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON autoresearch_collaborator USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE autoresearch_subscriber ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON autoresearch_subscriber USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE autoresearch_trigger ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON autoresearch_trigger USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE autoresearch_run ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON autoresearch_run USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE webhook_delivery ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON webhook_delivery USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());

-- The only cross-workspace reads and writes the Autoresearch needs. Each returns
-- ids; the work then runs in the row's workspace under row-level security.
CREATE FUNCTION autoresearch_webhook_lookup(h text) RETURNS TABLE (trigger_id uuid, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT id, workspace_id FROM autoresearch_trigger WHERE token_hash = h AND kind = 'webhook'
$$;

CREATE FUNCTION autoresearch_due(at timestamptz) RETURNS TABLE (workspace_id uuid, trigger_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT t.workspace_id, t.id FROM autoresearch_trigger t JOIN autoresearch a ON a.id = t.autoresearch_id
  WHERE t.kind = 'schedule' AND t.enabled AND a.status = 'active' AND t.next_run_at <= at
  ORDER BY t.next_run_at LIMIT 500
$$;

-- Runs a crash left between being saved and getting their card or agent run.
CREATE FUNCTION autoresearch_stale_runs(at timestamptz) RETURNS TABLE (workspace_id uuid, run_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT workspace_id, id FROM autoresearch_run
  WHERE status = 'queued' AND card_id IS NULL AND agent_run_id IS NULL AND created_at < at - interval '1 minute'
  ORDER BY created_at LIMIT 500
$$;

-- Runs whose status no longer matches their card or agent run, with the status and
-- reason they should have.
CREATE FUNCTION autoresearch_drift() RETURNS TABLE (workspace_id uuid, run_id uuid, status text, reason text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT r.workspace_id, r.id, f.status, f.reason
  FROM autoresearch_run r
  LEFT JOIN card c ON c.id = r.card_id
  LEFT JOIN agent_run t ON t.id = r.agent_run_id
  CROSS JOIN LATERAL (SELECT
    CASE
      WHEN c.id IS NOT NULL THEN CASE
        WHEN c.ending = 'released' THEN 'completed' WHEN c.ending <> '' THEN 'failed'
        WHEN c.flag <> '' THEN 'blocked' ELSE 'running' END
      ELSE CASE t.status
        WHEN 'completed' THEN 'completed' WHEN 'failed' THEN 'failed' WHEN 'cancelled' THEN 'failed' ELSE 'running' END
    END AS status,
    CASE
      WHEN c.id IS NOT NULL THEN CASE
        WHEN c.ending = 'released' THEN '' WHEN c.ending <> '' THEN 'card_' || c.ending
        WHEN c.flag <> '' THEN c.flag ELSE '' END
      ELSE CASE t.status
        WHEN 'failed' THEN coalesce(t.failure_reason, 'agent_error') WHEN 'cancelled' THEN 'cancelled' ELSE '' END
    END AS reason) f
  WHERE r.status IN ('running', 'blocked') AND (c.id IS NOT NULL OR t.id IS NOT NULL)
    AND (f.status <> r.status OR f.reason <> r.reason)
$$;

-- Leases up to n deliveries to one worker. SKIP LOCKED lets workers on several
-- servers share the queue; a worker that dies loses its lease after a minute.
CREATE FUNCTION claim_webhook_deliveries(n int) RETURNS TABLE (workspace_id uuid, delivery_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  UPDATE webhook_delivery d SET state = 'dispatching', attempts = d.attempts + 1, lease_until = now() + interval '1 minute'
  WHERE d.id IN (
    SELECT id FROM webhook_delivery
    WHERE (state = 'pending' AND (lease_until IS NULL OR lease_until <= now()))
       OR (state = 'dispatching' AND lease_until < now())
    ORDER BY received_at
    FOR UPDATE SKIP LOCKED
    LIMIT n)
  RETURNING d.workspace_id, d.id
$$;

REVOKE ALL ON FUNCTION autoresearch_webhook_lookup(text), autoresearch_due(timestamptz), autoresearch_stale_runs(timestamptz),
  autoresearch_drift(), claim_webhook_deliveries(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION autoresearch_webhook_lookup(text), autoresearch_due(timestamptz), autoresearch_stale_runs(timestamptz),
  autoresearch_drift(), claim_webhook_deliveries(int) TO lykeion_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON autoresearch, autoresearch_collaborator, autoresearch_subscriber, autoresearch_trigger,
  autoresearch_run, webhook_delivery TO lykeion_app;
