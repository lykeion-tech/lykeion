ALTER TABLE inbox_item
 ADD COLUMN title text NOT NULL DEFAULT '',
 ADD COLUMN context text NOT NULL DEFAULT '',
 ADD COLUMN deadline date;
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
 ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
 ('autoresearch_card','autoresearch_report','middle_score','blocked','no_tool','scoring_unavailable','done','waiting_for_machine','needs_decision','open_doubts','agent_decision','machine_offline','manual_decision')),
 ADD CONSTRAINT inbox_item_manual_shape CHECK (kind <> 'manual_decision' OR (
 length(title) BETWEEN 1 AND 200 AND title !~ E'[\\r\\n]' AND length(context) <= 4000
 AND options = '[]'::jsonb AND question_set IS NULL AND answers IS NULL
 AND expires_at IS NULL AND raised_run_id IS NULL));

CREATE FUNCTION preserve_manual_decision_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.kind='manual_decision' OR NEW.kind='manual_decision') AND
 (OLD.kind,OLD.user_id,OLD.workspace_id) IS DISTINCT FROM (NEW.kind,NEW.user_id,NEW.workspace_id) THEN
  RAISE EXCEPTION 'manual decision identity is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER preserve_manual_decision_identity BEFORE UPDATE ON inbox_item
 FOR EACH ROW EXECUTE FUNCTION preserve_manual_decision_identity();

DROP POLICY tenant ON inbox_item;
CREATE POLICY tenant ON inbox_item USING (
 workspace_id=app_workspace() AND (kind<>'manual_decision' OR
 (user_id=app_user() AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=inbox_item.workspace_id AND m.user_id=app_user())
 AND (card_id IS NULL OR EXISTS(SELECT 1 FROM card c WHERE c.workspace_id=inbox_item.workspace_id AND c.id=inbox_item.card_id)))))
 WITH CHECK (workspace_id=app_workspace() AND (kind<>'manual_decision' OR
 (user_id=app_user() AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=inbox_item.workspace_id AND m.user_id=app_user())
 AND (card_id IS NULL OR EXISTS(SELECT 1 FROM card c WHERE c.workspace_id=inbox_item.workspace_id AND c.id=inbox_item.card_id)))));

ALTER TABLE event ADD COLUMN recipient_id uuid REFERENCES "user"(id) ON DELETE CASCADE,
 ADD CONSTRAINT event_private_unlinked CHECK (recipient_id IS NULL OR card_id IS NULL);
CREATE INDEX event_recipient_cursor ON event(workspace_id,recipient_id,id) WHERE recipient_id IS NOT NULL;
DROP POLICY tenant ON event;
CREATE POLICY tenant ON event USING (workspace_id=app_workspace() AND
 (recipient_id IS NULL OR (recipient_id=app_user() AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=event.workspace_id AND m.user_id=app_user())
 AND (NOT (payload ? 'inbox_item_id') OR EXISTS(SELECT 1 FROM inbox_item i
 WHERE i.workspace_id=event.workspace_id AND i.id::text=event.payload->>'inbox_item_id'
 AND i.kind='manual_decision' AND i.user_id=app_user())))))
 WITH CHECK (workspace_id=app_workspace() AND (recipient_id IS NULL OR (recipient_id=app_user() AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=event.workspace_id AND m.user_id=app_user()))));
CREATE FUNCTION preserve_event_recipient() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.recipient_id IS DISTINCT FROM NEW.recipient_id THEN
  RAISE EXCEPTION 'event recipient is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER preserve_event_recipient BEFORE UPDATE ON event FOR EACH ROW EXECUTE FUNCTION preserve_event_recipient();

-- Private event identities never enter workspace maintenance feeds.
DROP TRIGGER lab_enqueue_event ON event;
CREATE TRIGGER lab_enqueue_event AFTER INSERT ON event FOR EACH ROW
 WHEN (NEW.recipient_id IS NULL) EXECUTE FUNCTION lab_enqueue_event();

CREATE OR REPLACE FUNCTION lab_events_after(consumer text, lim int) RETURNS SETOF event
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT e.* FROM public.event e
  WHERE e.recipient_id IS NULL AND e.id > COALESCE((SELECT c.last_event_id FROM public.lab_cursor c WHERE c.consumer = $1), 0)
  ORDER BY e.id LIMIT GREATEST(0, $2)
$$;
