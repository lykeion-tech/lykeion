DROP FUNCTION lab_pending_events(text,int), lab_ack_event(text,bigint), lab_index_workspaces_after(uuid,int), lab_index_workspace_exists(uuid);
DROP TRIGGER lab_enqueue_event ON event;
DROP FUNCTION lab_enqueue_event();
DROP TABLE lab_event_delivery,lab_event_consumer;
