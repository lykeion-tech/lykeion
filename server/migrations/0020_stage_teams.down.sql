DROP FUNCTION all_workspace_ids();
ALTER TABLE card DROP CONSTRAINT card_board_column_check;
ALTER TABLE card ALTER COLUMN board_column SET DEFAULT 'backlog';
ALTER TABLE field_pack DROP CONSTRAINT field_pack_workspace_id_name_key;
ALTER TABLE field_pack ADD CONSTRAINT field_pack_workspace_id_name_version_key UNIQUE (workspace_id, name, version);
