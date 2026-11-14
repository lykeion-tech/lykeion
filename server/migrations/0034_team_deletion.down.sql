DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM field_pack GROUP BY workspace_id, name HAVING count(*) > 1) THEN
    RAISE EXCEPTION 'cannot roll back Team deletion after a deleted Team name has been reused';
  END IF;
END $$;
DROP INDEX field_pack_live_name;
ALTER TABLE field_pack ADD CONSTRAINT field_pack_workspace_id_name_key UNIQUE (workspace_id, name);
ALTER TABLE agent DROP COLUMN team_deleted_at;
ALTER TABLE field_pack DROP COLUMN deleted_at;
