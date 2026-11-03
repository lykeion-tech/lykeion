ALTER TABLE lab_event_delivery ADD COLUMN workspace_id uuid;
UPDATE lab_event_delivery d SET workspace_id=e.workspace_id FROM event e WHERE e.id=d.event_id;
ALTER TABLE lab_event_delivery ALTER COLUMN workspace_id SET NOT NULL;
CREATE INDEX lab_event_delivery_workspace ON lab_event_delivery(consumer,workspace_id,event_id);
CREATE OR REPLACE FUNCTION lab_enqueue_event() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 INSERT INTO public.lab_event_delivery(consumer,event_id,workspace_id)
 SELECT c.consumer,NEW.id,NEW.workspace_id FROM public.lab_event_consumer c;
 RETURN NEW;
END $$;
CREATE FUNCTION lab_pending_workspace_events(consumer text, after_workspace uuid, lim int)
RETURNS TABLE(id bigint,workspace_id uuid,type text,card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 WITH chosen AS (SELECT COALESCE(
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.workspace_id>$2 ORDER BY d.workspace_id LIMIT 1),
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 ORDER BY d.workspace_id LIMIT 1)) AS ws)
 SELECT e.id,e.workspace_id,e.type,e.card_id FROM public.lab_event_delivery d
 JOIN chosen c ON c.ws=d.workspace_id JOIN public.event e ON e.id=d.event_id
 WHERE d.consumer=$1 ORDER BY d.event_id LIMIT LEAST(200,GREATEST(0,$3))
$$;
REVOKE ALL ON FUNCTION lab_pending_workspace_events(text,uuid,int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_pending_workspace_events(text,uuid,int) TO lykeion_app;

-- Every change to the shared decision projection dirties it in the same commit,
-- including user triage/keep/archive/revive and archive_stale_decisions().
CREATE FUNCTION lab_decision_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF NEW.card_id IS NOT NULL AND (jsonb_array_length(NEW.options)>0 OR NEW.question_set IS NOT NULL
  OR jsonb_array_length(OLD.options)>0 OR OLD.question_set IS NOT NULL) AND NEW IS DISTINCT FROM OLD THEN
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.workspace_id::text,0));
  INSERT INTO public.event(workspace_id,card_id,type,actor_type,payload)
  VALUES(NEW.workspace_id,NEW.card_id,'decision.updated','system',jsonb_build_object('inbox_item_id',NEW.id));
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION lab_decision_changed() FROM PUBLIC;
CREATE TRIGGER lab_decision_changed AFTER UPDATE ON inbox_item FOR EACH ROW EXECUTE FUNCTION lab_decision_changed();
