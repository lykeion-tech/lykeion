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


-- Maintenance can select candidates, but cannot read or mutate private notes
-- through ordinary workspace queries.
CREATE INDEX inbox_item_manual_due ON inbox_item(deadline,id)
 WHERE kind='manual_decision' AND status='open' AND reminded_at IS NULL AND deadline IS NOT NULL;
CREATE OR REPLACE FUNCTION due_decisions(at timestamptz) RETURNS TABLE (workspace_id uuid,id uuid,card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT workspace_id,id,card_id FROM inbox_item WHERE kind<>'manual_decision'
 AND status='open' AND expires_at IS NOT NULL AND expires_at<=at AND card_id IS NOT NULL
 ORDER BY expires_at,id LIMIT 100
$$;
CREATE OR REPLACE FUNCTION due_reminders(at timestamptz) RETURNS TABLE (workspace_id uuid,id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT workspace_id,id FROM inbox_item WHERE kind<>'manual_decision'
 AND status='open' AND expires_at IS NOT NULL AND reminded_at IS NULL AND expires_at<=at+interval '24 hours'
 ORDER BY expires_at,id LIMIT 100
$$;
CREATE OR REPLACE FUNCTION archive_stale_decisions(at timestamptz) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=public AS $$
 UPDATE inbox_item SET archived_at=at WHERE kind<>'manual_decision'
 AND archived_at IS NULL AND NOT kept AND status<>'open' AND touched_at<=at-interval '90 days'
$$;
CREATE FUNCTION due_manual_reminders(at timestamptz) RETURNS TABLE(workspace_id uuid,id uuid,user_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public AS $$
 SELECT i.workspace_id,i.id,i.user_id FROM inbox_item i JOIN "user" u ON u.id=i.user_id
 JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=i.user_id
 JOIN pg_timezone_names z ON z.name=u.timezone
 WHERE i.kind='manual_decision' AND i.status='open' AND i.resolved_at IS NULL
 AND i.reminded_at IS NULL AND i.deadline IS NOT NULL AND i.archived_at IS NULL
 AND (i.snoozed_until IS NULL OR i.snoozed_until<=at)
 AND at >= ((i.deadline-1)::timestamp AT TIME ZONE u.timezone)
 AND (i.card_id IS NULL OR EXISTS(SELECT 1 FROM card c WHERE c.id=i.card_id AND c.workspace_id=i.workspace_id))
 ORDER BY i.deadline,i.id LIMIT 100
$$;
CREATE FUNCTION remind_manual_decision(ws uuid,item uuid,at timestamptz) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE note inbox_item; creator uuid; zone text; linked uuid;
BEGIN
 PERFORM 1 FROM workspace WHERE id=ws FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 SELECT user_id,card_id INTO creator,linked FROM inbox_item WHERE workspace_id=ws AND id=item AND kind='manual_decision';
 IF NOT FOUND THEN RETURN false; END IF;
 -- Membership precedes the item lock, as it does in human mutations.
 PERFORM 1 FROM member WHERE workspace_id=ws AND user_id=creator FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 IF linked IS NOT NULL THEN
  PERFORM 1 FROM card WHERE id=linked AND workspace_id=ws FOR KEY SHARE;
  IF NOT FOUND THEN RETURN false; END IF;
 END IF;
 SELECT * INTO note FROM inbox_item WHERE workspace_id=ws AND id=item AND kind='manual_decision' FOR UPDATE;
 IF NOT FOUND OR note.status<>'open' OR note.resolved_at IS NOT NULL OR note.reminded_at IS NOT NULL
 OR note.deadline IS NULL OR note.archived_at IS NOT NULL OR note.snoozed_until>at THEN RETURN false; END IF;
 SELECT timezone INTO zone FROM "user" WHERE id=creator FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=zone) THEN RETURN false; END IF;
 IF at < ((note.deadline-1)::timestamp AT TIME ZONE zone) THEN RETURN false; END IF;
 IF note.card_id IS NOT NULL THEN
  PERFORM 1 FROM card WHERE id=note.card_id AND workspace_id=ws FOR KEY SHARE;
  IF NOT FOUND THEN RETURN false; END IF;
 END IF;
 -- Serialize event IDs with ordinary event writers so polling cannot skip
 -- an event committed after a newer cursor.
 PERFORM pg_advisory_xact_lock(hashtextextended(ws::text,0));
 UPDATE inbox_item SET reminded_at=at WHERE id=item AND workspace_id=ws;
 INSERT INTO event(workspace_id,type,actor_type,payload,recipient_id)
 VALUES(ws,'decision.reminder','system',jsonb_build_object('inbox_item_id',item::text),creator);
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION due_manual_reminders(timestamptz),remind_manual_decision(uuid,uuid,timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION due_manual_reminders(timestamptz),remind_manual_decision(uuid,uuid,timestamptz) TO lykeion_app;
