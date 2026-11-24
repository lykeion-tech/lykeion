-- What each lab read showed, so an answer can outlive a change to its source but
-- not a withdrawal. NULL keeps the exact-version check.
ALTER TABLE lab_read ADD COLUMN shown text[];
