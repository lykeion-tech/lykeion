-- Serving processes may verify an externally migrated database without being
-- granted direct access to the owner-only migration ledger.
CREATE FUNCTION public.lykeion_schema_compatible(required integer[]) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
  IF pg_catalog.current_setting('transaction_isolation') <> 'read committed' THEN
    RETURN false;
  END IF;
  IF required IS NULL OR cardinality(required) = 0 OR cardinality(required) > 1024
     OR array_position(required, NULL) IS NOT NULL
     OR EXISTS (SELECT 1 FROM pg_catalog.unnest(required) v WHERE v <= 0)
     OR (SELECT count(DISTINCT v) FROM pg_catalog.unnest(required) v) <> cardinality(required) THEN
    RETURN false;
  END IF;
  -- The migrator holds this session-level exclusive lock across all files.
  IF NOT pg_catalog.pg_try_advisory_xact_lock_shared(724501::bigint) THEN
    RETURN false;
  END IF;
  RETURN NOT EXISTS (
    SELECT 1 FROM pg_catalog.unnest(required) AS v(version)
    WHERE NOT EXISTS (SELECT 1 FROM public.schema_migrations m WHERE m.version=v.version)
  ) AND NOT EXISTS (
    SELECT 1 FROM public.schema_migrations m WHERE NOT (m.version = ANY(required))
  );
END $$;
REVOKE ALL ON FUNCTION public.lykeion_schema_compatible(integer[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.lykeion_schema_compatible(integer[]) TO lykeion_app;
