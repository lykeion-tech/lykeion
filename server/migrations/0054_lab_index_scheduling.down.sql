DROP TRIGGER lab_decision_changed ON inbox_item;
DROP FUNCTION lab_decision_changed();
DROP FUNCTION lab_pending_workspace_events(text,uuid,int);
CREATE OR REPLACE FUNCTION lab_enqueue_event() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 INSERT INTO public.lab_event_delivery(consumer,event_id)
 SELECT c.consumer,NEW.id FROM public.lab_event_consumer c;
 RETURN NEW;
END $$;
ALTER TABLE lab_event_delivery DROP COLUMN workspace_id;
