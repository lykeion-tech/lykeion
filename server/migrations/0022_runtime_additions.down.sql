ALTER TABLE attachment
  DROP CONSTRAINT attachment_size_bytes_check,
  ADD CONSTRAINT attachment_size_bytes_check CHECK (size_bytes BETWEEN 0 AND 104857600);

DROP INDEX attachment_service_name;
ALTER TABLE attachment
  DROP CONSTRAINT attachment_linked_service_check,
  DROP CONSTRAINT attachment_file_metadata_check,
  DROP COLUMN kind,
  DROP COLUMN service_name,
  DROP COLUMN service_start,
  DROP COLUMN service_client,
  DROP COLUMN service_env,
  DROP COLUMN inner_files,
  DROP COLUMN unpacked_bytes;
ALTER TABLE card DROP COLUMN env;
