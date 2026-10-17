package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func (f *fixture) member(t *testing.T, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	u, err := f.T.Store.Global().UpsertUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	err = f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: u.ID, Role: "member"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func (f *fixture) enqueueAutoresearch(t *testing.T, requester uuid.UUID, harness string) db.AgentRun {
	t.Helper()
	var tk db.AgentRun
	err := f.T.Store.InWorkspace(context.Background(), f.WS, requester, func(tx *store.Tx) error {
		var err error
		tk, err = f.T.EnqueueAutoresearch(context.Background(), tx, requester, AutoresearchAgentRun{Harness: harness, Prompt: "p", Skill: "# s"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestAutoresearchAgentRunIsClaimedOnlyByItsRequester(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	b := f.member(t, "b@lab.org")
	tk := f.enqueueAutoresearch(t, b, "claude")
	if tk.Kind != "autoresearch" || tk.CardID != nil || tk.RequesterID == nil || *tk.RequesterID != b || tk.ResponsibleUserID != b {
		t.Fatalf("enqueued: %+v", tk)
	}
	owner := f.runtime(t, f.Owner, "da", "claude")
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{owner}, 5); err != nil || len(got) != 0 {
		t.Fatal("another member's runtime claimed the autoresearch agent run")
	}
	codex := f.runtime(t, b, "db", "codex")
	if got, err := f.T.Claim(ctx, b, []RuntimeRef{codex}, 5); err != nil || len(got) != 0 {
		t.Fatal("a codex runtime claimed a claude agent run")
	}
	ref := f.runtime(t, b, "db", "claude")
	got, err := f.T.Claim(ctx, b, []RuntimeRef{ref}, 5)
	if err != nil || len(got) != 1 || got[0].AgentRun.ID != tk.ID {
		t.Fatalf("the requester's runtime: %+v %v", got, err)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if err := f.T.Start(ctx, b, ref, tk.ID); err != nil {
		t.Fatal(err)
	}
	row, err := f.T.Store.Global().LookupAgentRunToken(ctx, auth.Hash(got[0].Token))
	if err != nil || row.CardID != uuid.Nil || strings.Join(row.Scopes, ",") != "report:write,card:create,card:reopen" {
		t.Fatalf("token: %+v %v", row, err)
	}

	done, err := f.T.Complete(ctx, b, ref, tk.ID, Report{Usage: []agent.Usage{{Model: "m", OutputTokens: 3, CostUSD: usd(0.2)}}})
	if err != nil || done.Status != "completed" || done.CostUsd != 0.2 || f.settledCount() != 1 {
		t.Fatalf("settle: %+v %v, settled %d", done, err, f.settledCount())
	}
	var spent float64
	if err := f.Env.Owner.QueryRow(ctx, "SELECT spent_usd FROM card WHERE id = $1", f.Card).Scan(&spent); err != nil {
		t.Fatal(err)
	}
	if spent != 0 {
		t.Fatalf("a card-less agent run charged a card: %v", spent)
	}
}

func TestRoleAgentRunsAreClaimedAsBefore(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	b := f.member(t, "b@lab.org")
	f.enqueue(t, 1, "claude")
	f.enqueueAutoresearch(t, f.Owner, "claude")
	other := f.runtime(t, b, "db", "claude")
	if got, err := f.T.Claim(ctx, b, []RuntimeRef{other}, 5); err != nil || len(got) != 0 {
		t.Fatalf("another member claimed the owner's agent runs: %+v", got)
	}

	ref := f.runtime(t, f.Owner, "da", "claude")
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE card SET flag = 'escalated' WHERE id = $1", f.Card); err != nil {
		t.Fatal(err)
	}
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 5)
	if err != nil || len(got) != 1 || got[0].AgentRun.Kind != "autoresearch" {
		t.Fatalf("with the card flagged only the autoresearch agent run may go: %+v", got)
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE card SET flag = '' WHERE id = $1", f.Card); err != nil {
		t.Fatal(err)
	}
	got, err = f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 5)
	if err != nil || len(got) != 1 || got[0].AgentRun.Kind != "role" || got[0].AgentRun.CardID == nil || *got[0].AgentRun.CardID != f.Card {
		t.Fatalf("the role run: %+v", got)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if err := f.T.Start(ctx, f.Owner, ref, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	row, err := f.T.Store.Global().LookupAgentRunToken(ctx, auth.Hash(got[0].Token))
	if err != nil || row.CardID != f.Card || strings.Join(row.Scopes, ",") != ScopeRole {
		t.Fatalf("role token: %+v %v", row, err)
	}
}
