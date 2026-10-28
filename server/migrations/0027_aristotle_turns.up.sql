CREATE TABLE coresearch_turn_receipt (
 workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
 owner_id uuid NOT NULL REFERENCES "user",
 request_id uuid NOT NULL,
 target text NOT NULL,
 body text NOT NULL,
 response jsonb NOT NULL,
 PRIMARY KEY(workspace_id,owner_id,request_id),
 FOREIGN KEY(workspace_id,owner_id) REFERENCES member(workspace_id,user_id) ON DELETE CASCADE
);
ALTER TABLE coresearch_turn_receipt ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON coresearch_turn_receipt USING(workspace_id=app_workspace() AND owner_id=app_user()) WITH CHECK(workspace_id=app_workspace() AND owner_id=app_user());
GRANT SELECT,INSERT,UPDATE,DELETE ON coresearch_turn_receipt TO lykeion_app;
