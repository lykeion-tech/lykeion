UPDATE runtime SET max_concurrent = 2 WHERE max_concurrent IS NULL;
ALTER TABLE runtime ALTER COLUMN max_concurrent SET DEFAULT 2;
ALTER TABLE runtime ALTER COLUMN max_concurrent SET NOT NULL;
