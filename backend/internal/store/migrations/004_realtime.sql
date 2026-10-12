-- Leases are ephemeral operational state, never scientific history.
CREATE UNLOGGED TABLE realtime_leases (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id),
 user_id text NOT NULL REFERENCES users(id), session_digest text NOT NULL,
 auth_epoch bigint NOT NULL, expires_at timestamptz NOT NULL,
 presence_until timestamptz
);
CREATE INDEX realtime_leases_workspace ON realtime_leases(workspace_id, expires_at);
CREATE FUNCTION notify_workspace_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_notify('lykeion_events', '');
 RETURN NEW;
END;
$$;
CREATE TRIGGER workspace_event_hint AFTER INSERT ON events
 FOR EACH STATEMENT EXECUTE FUNCTION notify_workspace_event();
