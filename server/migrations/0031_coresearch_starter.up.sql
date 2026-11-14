ALTER TABLE coresearch_chat DROP CONSTRAINT coresearch_chat_project_mode_fkey;
ALTER TABLE coresearch_chat ADD CONSTRAINT coresearch_chat_project_mode_fkey
  FOREIGN KEY (workspace_id, project_id, mode)
  REFERENCES coresearch_project (workspace_id, id, mode) ON UPDATE CASCADE ON DELETE CASCADE;
