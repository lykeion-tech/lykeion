DROP FUNCTION remind_manual_decision(uuid,uuid,timestamptz);
DROP FUNCTION due_manual_reminders(timestamptz);
DROP INDEX inbox_item_manual_due;
CREATE OR REPLACE FUNCTION lab_events_after(consumer text, lim int) RETURNS SETOF event
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT e.* FROM public.event e
  WHERE e.id > COALESCE((SELECT c.last_event_id FROM public.lab_cursor c WHERE c.consumer = $1), 0)
  ORDER BY e.id LIMIT GREATEST(0, $2)
$$;

DROP TRIGGER lab_enqueue_event ON event;
CREATE TRIGGER lab_enqueue_event AFTER INSERT ON event FOR EACH ROW EXECUTE FUNCTION lab_enqueue_event();
DELETE FROM event WHERE recipient_id IS NOT NULL;
DELETE FROM inbox_item WHERE kind='manual_decision';
DROP TRIGGER preserve_event_recipient ON event;
DROP FUNCTION preserve_event_recipient();
DROP POLICY tenant ON event;
CREATE POLICY tenant ON event USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
ALTER TABLE event DROP COLUMN recipient_id;
DROP POLICY tenant ON inbox_item;
CREATE POLICY tenant ON inbox_item USING (workspace_id=app_workspace()) WITH CHECK (workspace_id=app_workspace());
DROP TRIGGER preserve_manual_decision_identity ON inbox_item;
DROP FUNCTION preserve_manual_decision_identity();
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_manual_shape, DROP CONSTRAINT inbox_item_kind_check,
 ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
 ('autoresearch_card','autoresearch_report','middle_score','blocked','no_tool','scoring_unavailable','done','waiting_for_machine','needs_decision','open_doubts','agent_decision','machine_offline')),
 DROP COLUMN title,DROP COLUMN context,DROP COLUMN deadline;


CREATE OR REPLACE FUNCTION due_decisions(at timestamptz) RETURNS TABLE (workspace_id uuid, id uuid, card_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT workspace_id, id, card_id FROM inbox_item
  WHERE status = 'open' AND expires_at IS NOT NULL AND expires_at <= at AND card_id IS NOT NULL
  ORDER BY expires_at, id LIMIT 100
$$;
CREATE OR REPLACE FUNCTION due_reminders(at timestamptz) RETURNS TABLE (workspace_id uuid, id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT workspace_id, id FROM inbox_item
  WHERE status = 'open' AND expires_at IS NOT NULL AND reminded_at IS NULL AND expires_at <= at + interval '24 hours'
  ORDER BY expires_at, id LIMIT 100
$$;
CREATE OR REPLACE FUNCTION archive_stale_decisions(at timestamptz) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
  UPDATE inbox_item SET archived_at = at
  WHERE archived_at IS NULL AND NOT kept AND status <> 'open' AND touched_at <= at - interval '90 days'
$$;
