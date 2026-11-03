-- Delivery rows are inserted in the producer's transaction. Unlike a high-water
-- event ID, their presence cannot skip a lower ID that commits later. Acknowledged
-- rows are removed; pending rows also disappear with their source event.
CREATE TABLE lab_event_consumer (consumer text PRIMARY KEY);
INSERT INTO lab_event_consumer VALUES ('indexer');
CREATE TABLE lab_event_delivery (
 consumer text NOT NULL REFERENCES lab_event_consumer ON DELETE CASCADE,
 event_id bigint NOT NULL REFERENCES event(id) ON DELETE CASCADE,
 PRIMARY KEY (consumer,event_id)
);
REVOKE ALL ON lab_event_consumer, lab_event_delivery FROM PUBLIC, lykeion_app;
CREATE FUNCTION lab_enqueue_event() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 INSERT INTO public.lab_event_delivery(consumer,event_id)
 SELECT c.consumer,NEW.id FROM public.lab_event_consumer c;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION lab_enqueue_event() FROM PUBLIC;
CREATE TRIGGER lab_enqueue_event AFTER INSERT ON event FOR EACH ROW EXECUTE FUNCTION lab_enqueue_event();
INSERT INTO lab_event_delivery SELECT c.consumer,e.id FROM lab_event_consumer c CROSS JOIN event e;

CREATE FUNCTION lab_pending_events(consumer text, lim int) RETURNS TABLE(id bigint, workspace_id uuid, type text, card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT e.id,e.workspace_id,e.type,e.card_id FROM public.lab_event_delivery d JOIN public.event e ON e.id=d.event_id
 WHERE d.consumer=$1 ORDER BY e.id LIMIT LEAST(200,GREATEST(0,$2))
$$;
CREATE FUNCTION lab_ack_event(consumer text, event_id bigint) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=public AS $$
 DELETE FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.event_id=$2
$$;
CREATE FUNCTION lab_index_workspaces_after(after_id uuid, lim int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT w.id FROM public.workspace w WHERE w.id>$1 ORDER BY w.id LIMIT LEAST(20,GREATEST(0,$2))
$$;
CREATE FUNCTION lab_index_workspace_exists(workspace_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT EXISTS(SELECT 1 FROM public.workspace w WHERE w.id=$1)
$$;
REVOKE ALL ON FUNCTION lab_pending_events(text,int), lab_ack_event(text,bigint), lab_index_workspaces_after(uuid,int), lab_index_workspace_exists(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_pending_events(text,int), lab_ack_event(text,bigint), lab_index_workspaces_after(uuid,int), lab_index_workspace_exists(uuid) TO lykeion_app;
