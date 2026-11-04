CREATE TABLE decision_draft_receipt (
 workspace_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 chat_id uuid PRIMARY KEY,
 decision_id uuid NOT NULL,
 input_hash bytea NOT NULL,
 FOREIGN KEY (workspace_id,chat_id,owner_id) REFERENCES lab_chat (workspace_id,id,owner_id) ON DELETE CASCADE
);
ALTER TABLE decision_draft_receipt ENABLE ROW LEVEL SECURITY;
CREATE POLICY decision_draft_receipt_private ON decision_draft_receipt
 USING (workspace_id=app_workspace() AND owner_id=app_user())
 WITH CHECK (workspace_id=app_workspace() AND owner_id=app_user());
GRANT SELECT, INSERT ON decision_draft_receipt TO lykeion_app;
