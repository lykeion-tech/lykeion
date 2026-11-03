-- Appending dirty refs under an inbox row lock must never acquire the workspace
-- event lock: card deletion takes that event lock before cascading inbox rows.
CREATE TABLE lab_fact_dirty (
 consumer text NOT NULL REFERENCES lab_event_consumer ON DELETE CASCADE,
 id bigserial NOT NULL,
 workspace_id uuid NOT NULL,
 ref text NOT NULL,
 card_id uuid,
 PRIMARY KEY(consumer,id)
);
CREATE INDEX lab_fact_dirty_workspace ON lab_fact_dirty(consumer,workspace_id,id);
REVOKE ALL ON lab_fact_dirty FROM PUBLIC,lykeion_app;
CREATE OR REPLACE FUNCTION lab_decision_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF (NEW.card_id IS NOT NULL OR OLD.card_id IS NOT NULL)
  AND (jsonb_array_length(NEW.options)>0 OR NEW.question_set IS NOT NULL
   OR jsonb_array_length(OLD.options)>0 OR OLD.question_set IS NOT NULL)
  AND NEW IS DISTINCT FROM OLD THEN
  INSERT INTO public.lab_fact_dirty(consumer,workspace_id,ref,card_id)
  SELECT c.consumer,NEW.workspace_id,'decision:'||NEW.id::text,COALESCE(NEW.card_id,OLD.card_id)
  FROM public.lab_event_consumer c;
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION lab_pending_workspace_work(consumer text,after_workspace uuid,lim int)
RETURNS TABLE(source text,id bigint,workspace_id uuid,type text,card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 WITH candidates AS (
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.workspace_id>$2 ORDER BY d.workspace_id LIMIT 1)
  UNION ALL (SELECT d.workspace_id FROM public.lab_fact_dirty d WHERE d.consumer=$1 AND d.workspace_id>$2 ORDER BY d.workspace_id LIMIT 1)
 ), fallback AS (
  (SELECT d.workspace_id FROM public.lab_event_delivery d WHERE d.consumer=$1 ORDER BY d.workspace_id LIMIT 1)
  UNION ALL (SELECT d.workspace_id FROM public.lab_fact_dirty d WHERE d.consumer=$1 ORDER BY d.workspace_id LIMIT 1)
 ), chosen AS (SELECT COALESCE((SELECT c.workspace_id FROM candidates c ORDER BY c.workspace_id LIMIT 1),
  (SELECT f.workspace_id FROM fallback f ORDER BY f.workspace_id LIMIT 1)) AS ws), work AS (
  (SELECT 'event'::text AS source,e.id,e.workspace_id,e.type,e.card_id FROM public.lab_event_delivery d
   JOIN chosen c ON c.ws=d.workspace_id JOIN public.event e ON e.id=d.event_id WHERE d.consumer=$1
   ORDER BY d.event_id LIMIT LEAST(200,GREATEST(0,$3)))
  UNION ALL
  (SELECT 'dirty'::text,d.id,d.workspace_id,'decision.updated'::text,d.card_id FROM public.lab_fact_dirty d
   JOIN chosen c ON c.ws=d.workspace_id WHERE d.consumer=$1 ORDER BY d.id LIMIT LEAST(200,GREATEST(0,$3)))
 ) SELECT * FROM work ORDER BY id,source LIMIT LEAST(200,GREATEST(0,$3))
$$;
CREATE FUNCTION lab_ack_work(consumer text,event_ids bigint[],dirty_ids bigint[]) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF COALESCE(cardinality($2),0)+COALESCE(cardinality($3),0)>200 THEN
  RAISE EXCEPTION 'at most 200 captured work IDs may be acknowledged';
 END IF;
 DELETE FROM public.lab_event_delivery d WHERE d.consumer=$1 AND d.event_id=ANY($2);
 DELETE FROM public.lab_fact_dirty d WHERE d.consumer=$1 AND d.id=ANY($3);
END $$;
REVOKE ALL ON FUNCTION lab_pending_workspace_work(text,uuid,int),lab_ack_work(text,bigint[],bigint[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION lab_pending_workspace_work(text,uuid,int),lab_ack_work(text,bigint[],bigint[]) TO lykeion_app;
