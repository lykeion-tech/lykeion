ALTER TABLE field_pack ADD COLUMN deleted_at timestamptz;
ALTER TABLE agent ADD COLUMN team_deleted_at timestamptz;
ALTER TABLE field_pack DROP CONSTRAINT field_pack_workspace_id_name_key;
CREATE UNIQUE INDEX field_pack_live_name ON field_pack (workspace_id, name) WHERE deleted_at IS NULL;
