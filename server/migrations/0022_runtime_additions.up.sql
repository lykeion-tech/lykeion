ALTER TABLE card ADD COLUMN env jsonb NOT NULL DEFAULT '{}'
  CHECK (jsonb_typeof(env) = 'object' AND NOT jsonb_path_exists(env, 'strict $.* ? (@.type() != "string")'));

ALTER TABLE attachment
  DROP CONSTRAINT attachment_size_bytes_check,
  ADD CONSTRAINT attachment_size_bytes_check CHECK (size_bytes BETWEEN 0 AND 5368709120),
  ADD COLUMN kind text NOT NULL DEFAULT 'file' CHECK (kind IN ('file', 'service')),
  ADD COLUMN service_name text,
  ADD COLUMN service_start text,
  ADD COLUMN service_client text,
  ADD COLUMN service_env text[] NOT NULL DEFAULT '{}',
  ADD COLUMN inner_files integer NOT NULL DEFAULT 0 CHECK (inner_files BETWEEN 0 AND 20000),
  ADD COLUMN unpacked_bytes bigint NOT NULL DEFAULT 0 CHECK (unpacked_bytes >= 0),
  ADD CONSTRAINT attachment_linked_service_check CHECK (kind <> 'service' OR card_id IS NULL OR (service_name IS NOT NULL AND service_start IS NOT NULL)),
  ADD CONSTRAINT attachment_file_metadata_check CHECK (kind <> 'file' OR (service_name IS NULL AND service_start IS NULL AND service_client IS NULL AND cardinality(service_env) = 0));

CREATE UNIQUE INDEX attachment_service_name ON attachment (workspace_id, card_id, service_name) WHERE kind = 'service';
