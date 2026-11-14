ALTER TABLE field_pack ADD COLUMN visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'workspace'));
