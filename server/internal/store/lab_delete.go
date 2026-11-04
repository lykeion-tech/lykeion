package store

import (
	"context"
	"github.com/google/uuid"
)

// PurgeDeletedLabChat erases text but keeps run and accounting identities. The
// caller owns the chat and holds the execution and run locks. Database guards
// apply the same scrubbing to later daemon receipts and reports.
func (t *Tx) PurgeDeletedLabChat(ctx context.Context, chat uuid.UUID) error {
	for _, query := range []string{
		`UPDATE agent_run SET prompt='',skill='',error='',handoff_note='',session_id='',input_outcomes='{}' WHERE workspace_id=$1 AND lab_chat_id=$2 AND requester_id=app_user()`,
		`DELETE FROM agent_run_message m USING agent_run r WHERE m.workspace_id=$1 AND r.workspace_id=m.workspace_id AND r.id=m.agent_run_id AND r.lab_chat_id=$2 AND r.requester_id=app_user()`,
		`DELETE FROM lab_read m USING agent_run r WHERE m.workspace_id=$1 AND r.workspace_id=m.workspace_id AND r.id=m.agent_run_id AND r.lab_chat_id=$2 AND r.requester_id=app_user()`,
		`UPDATE agent_run_receipt m SET result=result - ARRAY['prompt','skill','error','handoff_note','session_id','input_outcomes'] FROM agent_run r WHERE m.workspace_id=$1 AND r.workspace_id=m.workspace_id AND r.id=m.agent_run_id AND r.lab_chat_id=$2 AND r.requester_id=app_user()`,
		`UPDATE agent_run_settlement m SET result=result - ARRAY['prompt','skill','error','handoff_note','session_id','input_outcomes'] FROM agent_run r WHERE m.workspace_id=$1 AND r.workspace_id=m.workspace_id AND r.id=m.agent_run_id AND r.lab_chat_id=$2 AND r.requester_id=app_user()`,
	} {
		if _, err := t.raw.Exec(ctx, query, t.WS, chat); err != nil {
			return err
		}
	}
	return nil
}
