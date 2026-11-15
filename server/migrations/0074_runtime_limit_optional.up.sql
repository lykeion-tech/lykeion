-- A runtime's own limit on runs at once is optional, as in Multica: with none,
-- its daemon's slots decide. Runtimes still at the old default of 2 lose it;
-- any other value was chosen by someone and stays.
ALTER TABLE runtime ALTER COLUMN max_concurrent DROP NOT NULL;
ALTER TABLE runtime ALTER COLUMN max_concurrent SET DEFAULT NULL;
UPDATE runtime SET max_concurrent = NULL WHERE max_concurrent = 2;
