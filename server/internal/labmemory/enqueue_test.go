package labmemory

import (
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

func TestOnlyTheAskersRuntimeClaimsIt(t *testing.T) {
	f, runs, chat := labQueue(t)
	other := uuid.New()
	f.exec(`INSERT INTO "user"(id,email,name) VALUES($1,'other@example.org','Other')`, other)
	f.exec("INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')", f.ws, other)
	register := func(owner uuid.UUID, name, harness string) service.RuntimeRef {
		rows, err := runs.Register(t.Context(), owner, f.ws, name, name, []string{harness}, nil, agent.Platform{OS: "linux"})
		if err != nil {
			t.Fatal(err)
		}
		return service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rows[0].ID}
	}
	foreign := register(other, "shared", "claude")
	f.exec("UPDATE runtime SET visibility='workspace' WHERE id=$1", foreign.RuntimeID)
	generic := register(f.user, "generic", "claude")
	exact := register(f.user, "exact", "claude")
	wrong := register(f.user, "wrong", "codex")
	f.exec(`UPDATE runtime SET models='[{"value":"frozen"}]' WHERE id=$1`, exact.RuntimeID)
	first := enqueueQuestion(t, f, runs, chat, "first", "frozen")
	// Even an agent with general-purpose capabilities gets no such claims.
	f.exec(`INSERT INTO agent_capabilities(workspace_id,agent_id,skills,connectors) VALUES($1,$2,'["private-skill"]','[{"name":"external"}]')`, f.ws, *first.AgentID)
	second := enqueueQuestion(t, f, runs, chat, "second", "frozen")
	claim := func(owner uuid.UUID, ref service.RuntimeRef) []service.Claimed {
		got, err := runs.Claim(t.Context(), owner, []service.RuntimeRef{ref}, 1, service.ClaimOptions{GuestIsolation: agent.GuestIsolationProtocol})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, c := range [][]service.Claimed{claim(other, foreign), claim(f.user, generic), claim(f.user, wrong)} {
		if len(c) != 0 {
			t.Fatal("unexpected claim", c)
		}
	}
	got := claim(f.user, exact)
	if len(got) != 1 {
		t.Fatal("missing claim")
	}
	c := got[0]
	if c.AgentRun.ID != first.ID || c.AgentRun.RequestedModel != "frozen" || c.CallToken != "" || c.Token == "" || c.MCPURL != "lab" || len(c.Skills)+len(c.MCP)+len(c.Connectors)+len(c.WorkspaceConnectors) != 0 {
		t.Fatal("wrong claim scope/model")
	}
	if len(claim(f.user, generic)) != 0 {
		t.Fatal("later question overtook active turn")
	}
	f.exec("UPDATE runtime SET unhealthy_reason='sign_in' WHERE id=$1", exact.RuntimeID)
	f.exec("UPDATE agent_run SET status='completed' WHERE id=$1", first.ID)
	f.exec("INSERT INTO lab_message(workspace_id,owner_id,chat_id,agent_run_id,kind,body) VALUES($1,$2,$3,$4,'answer','answered while queued')", f.ws, f.user, chat.ID, first.ID)
	if got = claim(f.user, generic); len(got) != 1 || got[0].AgentRun.ID != second.ID || got[0].AgentRun.RequestedModel != "" || strings.Contains(got[0].AgentRun.Prompt, "answered while queued") {
		t.Fatal("missing CLI default fallback", got)
	}
}

func TestAristotleRemovalCancelsWorkAcrossRejoin(t *testing.T) {
	f, runs, chat := labQueue(t)
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
	claimed, err := runs.Claim(ctx, f.user, []service.RuntimeRef{ref}, 1)
	if err != nil || len(claimed) != 1 || claimed[0].AgentRun.ID != first.ID {
		t.Fatal(err, len(claimed))
	}
	ref.Attempt = claimed[0].AgentRun.Attempt
	if err = runs.Start(ctx, f.user, ref, first.ID); err != nil {
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
	f.exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, f.ws, f.user)
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		var status string
		var tokens, settlements int
		err = f.env.Owner.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id),(SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=r.id) FROM agent_run r WHERE id=$1`, id).Scan(&status, &tokens, &settlements)
		if err != nil || status != "cancelled" || tokens != 0 || settlements != 1 {
			t.Fatalf("status=%s tokens=%d settlements=%d err=%v", status, tokens, settlements, err)
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

func TestLabCodexQuestionIsRefusedBeforePersistence(t *testing.T) {
	f, runs, chat := labQueue(t)
	err := runs.Store.InWorkspace(t.Context(), f.ws, f.user, func(tx *store.Tx) error {
		_, err := Enqueue(t.Context(), tx, runs, chat, f.user, "private", nil, "codex", "")
		return err
	})
	if !errors.Is(err, agent.ErrLabHarness) {
		t.Fatal(err)
	}
	var n int
	err = f.env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM agent_run WHERE lab_chat_id=$1)+(SELECT count(*) FROM lab_message WHERE chat_id=$1)`, chat.ID).Scan(&n)
	if err != nil || n != 0 {
		t.Fatal(err, n)
	}
}
