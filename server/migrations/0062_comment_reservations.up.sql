-- Historical delivery records remain readable; they are not evidence of a new launch.
ALTER TABLE comment ADD CONSTRAINT comment_workspace_id_key UNIQUE (workspace_id, id);
CREATE TABLE comment_reservation (
 workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
 comment_id uuid NOT NULL,
 agent_run_id uuid NOT NULL,
 attempt int NOT NULL CHECK (attempt > 0),
 acknowledged_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (workspace_id, comment_id),
 FOREIGN KEY (workspace_id, comment_id) REFERENCES comment (workspace_id, id) ON DELETE CASCADE,
 FOREIGN KEY (agent_run_id, workspace_id) REFERENCES agent_run (id, workspace_id) ON DELETE CASCADE
);
ALTER TABLE comment_reservation ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON comment_reservation USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON comment_reservation TO lykeion_app;
CREATE INDEX comment_reservation_run ON comment_reservation(workspace_id, agent_run_id, attempt);

-- Old enqueue receipts for work which has not started cannot certify delivery.
-- Preserve the records as provenance but move their delivery state to pending.
INSERT INTO comment_reservation(workspace_id, comment_id, agent_run_id, attempt)
SELECT DISTINCT ON (r.workspace_id, c.id) r.workspace_id, c.id, ar.id,
 CASE WHEN ar.status='queued' THEN ar.attempt+1 ELSE ar.attempt END
FROM record r JOIN agent_run ar ON ar.id=r.agent_run_id AND ar.workspace_id=r.workspace_id
JOIN comment c ON c.workspace_id=r.workspace_id AND c.id::text=r.body->>'comment_id'
WHERE r.kind='comment_delivery' AND r.body->>'how'='next_run' AND ar.status IN ('queued','dispatched')
ORDER BY r.workspace_id,c.id,r.created_at,r.id
ON CONFLICT DO NOTHING;
UPDATE record r SET body=r.body || '{"historical_unconfirmed":true}'::jsonb
FROM agent_run ar
WHERE r.kind='comment_delivery' AND r.body->>'how'='next_run' AND ar.id=r.agent_run_id
 AND ar.workspace_id=r.workspace_id AND ar.status IN ('queued','dispatched');
