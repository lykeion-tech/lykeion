package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func labQueue(t *testing.T) (*fixture, *service.AgentRuns, db.LabChat) {
	f := setup(t)
	runs := &service.AgentRuns{Store: &store.Store{LessonsEnabled: true, Pool: f.env.App}, MCPURL: "sources", LabMCPURL: "lab"}
	chat := db.LabChat{ID: uuid.New(), WorkspaceID: f.ws, OwnerID: f.user}
	f.exec("INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)", chat.ID, f.ws, f.user)
	return f, runs, chat
}

func enqueueQuestion(t *testing.T, f *fixture, runs *service.AgentRuns, chat db.LabChat, question, model string) db.AgentRun {
	t.Helper()
	var run db.AgentRun
	err := runs.Store.InWorkspace(t.Context(), f.ws, f.user, func(tx *store.Tx) error {
		var err error
		run, err = Enqueue(t.Context(), tx, runs, chat, f.user, question, &f.study, "claude", model)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestEnqueueMakesAnAristotleRunForTheAsker(t *testing.T) {
	f, runs, chat := labQueue(t)
	f.exec("INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body) VALUES($1,$2,$3,'answer','Earlier answer')", f.ws, chat.ID, f.user)
	run := enqueueQuestion(t, f, runs, chat, "private question", "exact")
	if run.Kind != "aristotle" || run.Role != "aristotle" || run.RequesterID == nil || *run.RequesterID != f.user || run.ResponsibleUserID != f.user || run.LabChatID == nil || *run.LabChatID != chat.ID || run.AgentID == nil || run.ContinuityScope != "lab-chat:"+chat.ID.String() || run.RequestedModel != "exact" || !strings.HasPrefix(run.Prompt, "private question") || !strings.Contains(run.Prompt, "Context study: study:"+f.study.String()) || strings.Contains(run.Prompt, "Earlier answer") || !strings.Contains(run.Skill, Protocol()) {
		t.Fatal(run)
	}
	var payload map[string]string
	var body string
	if err := f.env.Owner.QueryRow(t.Context(), "SELECT body FROM lab_message WHERE agent_run_id=$1 AND kind='question' AND context_card_id=$2", run.ID, f.study).Scan(&body); err != nil || body != "private question" {
		t.Fatal(err, body)
	}
	var raw []byte
	if err := f.env.Owner.QueryRow(t.Context(), "SELECT payload FROM event WHERE workspace_id=$1 AND type='lab.message.created'", f.ws).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload) != 2 || payload["chat_id"] != chat.ID.String() || payload["owner_id"] != f.user.String() {
		t.Fatal(err, payload)
	}
}

// Exercise the public admission and launch paths in settlement/history fixtures.
func historicalClaim(t *testing.T, f *fixture, runs *service.AgentRuns, ref service.RuntimeRef) ([]service.Claimed, error) {
	t.Helper()
	return runs.Claim(t.Context(), f.user, []service.RuntimeRef{ref}, 1)
}
func historicalStart(ctx context.Context, runs *service.AgentRuns, user uuid.UUID, ref service.RuntimeRef, id uuid.UUID) (string, error) {
	return runs.StartLab(ctx, user, ref, id, LaunchPrompt)
}

func TestAristotleRunIsClaimedByOwnRuntime(t *testing.T) {
	f, runs, chat := labQueue(t)
	ctx := t.Context()
	rows, err := runs.Register(ctx, f.user, f.ws, "personal", "machine", []string{"claude"}, nil, agent.Platform{OS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rows[0].ID}
	run := enqueueQuestion(t, f, runs, chat, "private question", "")
	got, err := runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 || got[0].AgentRun.ID != run.ID {
		t.Fatal(err, len(got))
	}
	var status string
	var tokens int
	err = f.env.Owner.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id) FROM agent_run r WHERE id=$1`, run.ID).Scan(&status, &tokens)
	if err != nil || status != "dispatched" || tokens != 1 {
		t.Fatal(err, status, tokens)
	}
}

func TestAristotleRemovalCancelsWorkAcrossRejoin(t *testing.T) {
	f, runs, chat := labQueue(t)
	runs.OnSettlement = (Answers{Store: runs.Store}).OnSettlement
	ctx := t.Context()
	owner := uuid.New()
	f.exec(`INSERT INTO "user"(id,email,name) VALUES($1,'remover@example.org','Owner')`, owner)
	f.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, f.ws, owner)
	rows, err := runs.Register(ctx, f.user, f.ws, "personal", "machine", []string{"claude"}, nil, agent.Platform{OS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rows[0].ID}
	first := enqueueQuestion(t, f, runs, chat, "active", "")
	second := enqueueQuestion(t, f, runs, chat, "queued", "")
	claimed, err := historicalClaim(t, f, runs, ref)
	if err != nil || len(claimed) != 1 || claimed[0].AgentRun.ID != first.ID {
		t.Fatal(err, len(claimed))
	}
	ref.Attempt = claimed[0].AgentRun.Attempt
	f.exec(`INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES('history-fixture',$1,$2,'aristotle',$3,ARRAY['aristotle:ask'],now()+interval '1 hour')`, f.ws, first.ID, ref.Attempt)
	if _, err = historicalStart(ctx, runs, f.user, ref, first.ID); err != nil {
		t.Fatal(err)
	}
	err = runs.Store.InWorkspace(ctx, f.ws, owner, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, f.ws); err != nil {
			return err
		}
		if err := runs.RemoveMemberSharing(ctx, tx, owner, f.user); err != nil {
			return err
		}
		_, err := tx.Q.RemoveMember(ctx, db.RemoveMemberParams{WorkspaceID: f.ws, UserID: f.user})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runs.RecoverSettlements(ctx); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, f.ws, f.user)
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		var status string
		var tokens, settlements int
		err = f.env.Owner.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id),(SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=r.id) FROM agent_run r WHERE id=$1`, id).Scan(&status, &tokens, &settlements)
		if err != nil || status != "cancelled" || tokens != 0 || settlements != 1 {
			t.Fatalf("status=%s tokens=%d settlements=%d err=%v", status, tokens, settlements, err)
		}
	}
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		var failures int
		if err := f.env.Owner.QueryRow(ctx, "SELECT count(*) FROM lab_message WHERE agent_run_id=$1 AND kind='failure' AND body='Aristotle could not answer: the run was cancelled.'", id).Scan(&failures); err != nil || failures != 1 {
			t.Fatal(err, failures)
		}
	}
	if got, err := runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1); err != nil || len(got) != 0 {
		t.Fatal(err, len(got))
	}
	for range 2 {
		late, err := runs.Complete(ctx, f.user, ref, first.ID, service.Report{})
		if err != nil || late.Status != "cancelled" {
			t.Fatal(err, late.Status)
		}
	}
}

func TestUnsupportedLabQuestionsRefusedBeforePersistence(t *testing.T) {
	for _, harness := range []string{"cursor", "antigravity"} {
		t.Run(harness, func(t *testing.T) {
			f, runs, chat := labQueue(t)
			err := runs.Store.InWorkspace(t.Context(), f.ws, f.user, func(tx *store.Tx) error {
				_, err := Enqueue(t.Context(), tx, runs, chat, f.user, "private", nil, harness, "")
				return err
			})
			if !errors.Is(err, agent.LabAvailability(harness)) {
				t.Fatal(err)
			}
			var n int
			err = f.env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM agent_run WHERE lab_chat_id=$1)+(SELECT count(*) FROM lab_message WHERE chat_id=$1)`, chat.ID).Scan(&n)
			if err != nil || n != 0 {
				t.Fatal(err, n)
			}
		})
	}
}

func TestUnsupportedLabHarnessCancelsOpenRunsAndRevokesTokens(t *testing.T) {
	f, runs, chat := labQueue(t)
	ctx := t.Context()
	ordinary := uuid.New()
	f.exec(`INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt)
 VALUES($1,$2,$3,$4,'grounding',1,'scoper','claude','ordinary')`, ordinary, f.ws, f.study, f.user)
	ids := map[string]uuid.UUID{}
	for _, status := range []string{"queued", "dispatched", "running"} {
		r := enqueueQuestion(t, f, runs, chat, status, "")
		ids[status] = r.ID
		f.exec("UPDATE agent_run SET status=$2,attempt=1,harness='cursor' WHERE id=$1", r.ID, status)
		f.exec(`INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,'aristotle',1,ARRAY['aristotle:ask'],now()+interval '1 hour')`, r.ID.String(), f.ws, r.ID)
	}
	for range 2 {
		if err := runs.CancelUnavailableLabRuns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var ordinaryStatus string
	if err := f.env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", ordinary).Scan(&ordinaryStatus); err != nil || ordinaryStatus != "queued" {
		t.Fatal(err, ordinaryStatus)
	}
	for initial, id := range ids {
		var status string
		var tokens, settlements, events int
		err := f.env.Owner.QueryRow(ctx, `SELECT r.status,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id),
 (SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=r.id),
 (SELECT count(*) FROM event WHERE workspace_id=r.workspace_id AND type='agent_run.cancelled' AND payload->>'agent_run_id'=r.id::text)
 FROM agent_run r WHERE id=$1`, id).Scan(&status, &tokens, &settlements, &events)
		if err != nil || status != "cancelled" || tokens != 0 || settlements != 1 || events != 1 {
			t.Fatal(initial, err, status, tokens, settlements, events)
		}
	}
}

// Reconciliation must leave supported queued, dispatched and running work intact.
func TestSupportedLabRunsSurviveAvailabilityReconciliation(t *testing.T) {
	f, runs, chat := labQueue(t)
	ids := map[uuid.UUID]string{}
	for _, harness := range []string{"claude", "codex"} {
		for _, status := range []string{"queued", "dispatched", "running"} {
			r := enqueueQuestion(t, f, runs, chat, "supported", "")
			f.exec("UPDATE agent_run SET status=$2,harness=$3,attempt=1 WHERE id=$1", r.ID, status, harness)
			f.exec(`INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,'aristotle',1,ARRAY['aristotle:ask'],now()+interval '1 hour')`, r.ID.String(), f.ws, r.ID)
			ids[r.ID] = status
		}
	}
	if err := runs.CancelUnavailableLabRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	for id, want := range ids {
		var status string
		var tokens, settlements int
		err := f.env.Owner.QueryRow(t.Context(), `SELECT status,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id),(SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=r.id) FROM agent_run r WHERE id=$1`, id).Scan(&status, &tokens, &settlements)
		if err != nil || status != want || tokens != 1 || settlements != 0 {
			t.Fatal(err, status, tokens, settlements)
		}
	}
}
