-- A submitted manifest is an immutable record. Supersession/revocation will add
-- separate lifecycle records; neither may rewrite reviewed bytes in place.
CREATE FUNCTION protect_sealed_artifact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'sealed artifacts cannot be changed or deleted' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER sealed_artifact_immutable BEFORE UPDATE OR DELETE ON artifacts
 FOR EACH ROW EXECUTE FUNCTION protect_sealed_artifact();
