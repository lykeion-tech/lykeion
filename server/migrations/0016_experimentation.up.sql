-- Each language base's state on the runtime's machine, as its daemon last
-- reported it: "ready", "building" or "failed: <reason>", keyed by language.
-- A language the daemon has not tried yet is absent.
ALTER TABLE runtime
  ADD COLUMN bases jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(bases) = 'object' AND pg_column_size(bases) <= 4096);
