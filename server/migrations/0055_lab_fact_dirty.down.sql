DROP FUNCTION lab_pending_workspace_work(text,uuid,int),lab_ack_work(text,bigint[],bigint[]);
DROP TABLE lab_fact_dirty;
CREATE OR REPLACE FUNCTION lab_decision_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF NEW.card_id IS NOT NULL AND (jsonb_array_length(NEW.options)>0 OR NEW.question_set IS NOT NULL
  OR jsonb_array_length(OLD.options)>0 OR OLD.question_set IS NOT NULL) AND NEW IS DISTINCT FROM OLD THEN
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.workspace_id::text,0));
  INSERT INTO public.event(workspace_id,card_id,type,actor_type,payload)
  VALUES(NEW.workspace_id,NEW.card_id,'decision.updated','system',jsonb_build_object('inbox_item_id',NEW.id));
 END IF;
 RETURN NEW;
END $$;
