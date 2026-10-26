package service

import (
	"context"
	"testing"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestCoresearchClaimRechecksMembership(t *testing.T) {
	f := setup(t)
	var chat db.CoresearchChat
	var run db.AgentRun
	e := f.T.Store.InWorkspace(t.Context(), f.WS, f.Owner, func(tx *store.Tx) error {
		p, e := tx.Q.CreateCoresearchProject(t.Context(), "Project")
		if e != nil {
			return e
		}
		chat, e = tx.Q.CreateCoresearchChat(t.Context(), db.CreateCoresearchChatParams{WorkspaceID: f.WS, ProjectID: p, OwnerID: f.Owner, Cli: "claude"})
		if e != nil {
			return e
		}
		run, e = tx.Q.EnqueueCoresearchAgentRun(t.Context(), db.EnqueueCoresearchAgentRunParams{WorkspaceID: f.WS, OwnerID: f.Owner, Harness: "claude", Prompt: "prompt", CoresearchChatID: &chat.ID, ContinuityScope: "coresearch:" + chat.ID.String()})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	ref := f.runtime(t, f.Owner, "owner", "claude")
	f.T.afterClaim = func(ctx context.Context, tx *store.Tx, r db.AgentRun) error {
		return tx.Q.DeleteCoresearchMember(ctx, db.DeleteCoresearchMemberParams{WorkspaceID: f.WS, ProjectID: chat.ProjectID, UserID: f.Owner})
	}
	claimed, e := f.T.Claim(t.Context(), f.Owner, []RuntimeRef{ref}, 1)
	if e != nil || len(claimed) != 0 {
		t.Fatalf("%+v %v", claimed, e)
	}
	var status, reason string
	var attempt, tokens, accepted int
	var daemon *string
	e = f.Env.Owner.QueryRow(t.Context(), `SELECT a.status,a.failure_reason,a.attempt,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=a.id),(SELECT count(*) FROM agent_run_machine_attempt WHERE agent_run_id=a.id),c.daemon_id FROM agent_run a JOIN coresearch_chat c ON c.id=a.coresearch_chat_id WHERE a.id=$1`, run.ID).Scan(&status, &reason, &attempt, &tokens, &accepted, &daemon)
	if e != nil {
		t.Fatal(e)
	}
	if status != "failed" || reason != "coresearch_access_revoked" || attempt != 0 || tokens != 0 || accepted != 0 || daemon != nil {
		t.Fatalf("%s %s attempt=%d tokens=%d accepted=%d daemon=%v", status, reason, attempt, tokens, accepted, daemon)
	}
}
func TestCoresearchRevocationReasonIsServerOnly(t *testing.T) {
	r := Report{FailureReason: "coresearch_access_revoked"}
	if e := r.check(false); e != ErrBadReport {
		t.Fatal(e)
	}
}
