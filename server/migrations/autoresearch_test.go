package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func seedWorkspace(t *testing.T, env *testutil.Env) (u, ws, card uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	u, ws = uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column)
		VALUES ($1, 1, 't', 'q', $2, 'grounding') RETURNING id`, ws, u).Scan(&card); err != nil {
		t.Fatal(err)
	}
	return u, ws, card
}

func TestAgentRunKindShape(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	ins := `INSERT INTO agent_run (workspace_id, card_id, kind, requester_id, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, $3, $4, $5, '', 0, 'autoresearch', 'claude', 'p')`
	for _, c := range []struct {
		name            string
		card, requester *uuid.UUID
		kind            string
		ok              bool
	}{
		{"a role run on a card", &card, nil, "role", true},
		{"a role run without a card", nil, nil, "role", false},
		{"an autoresearch agent run for a requester", nil, &u, "autoresearch", true},
		{"an autoresearch agent run on a card", &card, &u, "autoresearch", false},
		{"an autoresearch agent run for nobody", nil, nil, "autoresearch", false},
		{"an unknown kind", nil, &u, "ask", false},
	} {
		if _, err := env.Owner.Exec(ctx, ins, ws, c.card, c.kind, c.requester, u); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestAgentRunTokenLookupCarriesScopes(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	var ap, role uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id, kind, requester_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, 'autoresearch', $2, $2, '', 0, 'autoresearch', 'claude', 'p', 'running', 1) RETURNING id`, ws, u).Scan(&ap); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p', 'running', 1) RETURNING id`, ws, card, u).Scan(&role); err != nil {
		t.Fatal(err)
	}
	tok := `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now() + interval '1 hour')`
	for _, k := range []struct {
		hash    string
		run     uuid.UUID
		card    *uuid.UUID
		role    string
		attempt int
		scopes  []string
	}{
		{"ha", ap, nil, "autoresearch", 1, []string{"report:write", "card:create"}},
		{"hr", role, &card, "scoper", 1, []string{"card:role"}},
		{"hs", ap, nil, "autoresearch", 2, []string{"report:write", "card:create"}}, // a stale attempt
		{"hw", ap, nil, "scoper", 1, []string{"report:write", "card:create"}},       // another role
	} {
		if _, err := env.Owner.Exec(ctx, tok, k.hash, ws, k.run, k.card, k.role, k.attempt, k.scopes); err != nil {
			t.Fatalf("token %s: %v", k.hash, err)
		}
	}

	var cardID *uuid.UUID
	var scopes []string
	if err := env.App.QueryRow(ctx, "SELECT card_id, scopes FROM agent_run_token_lookup('ha')").Scan(&cardID, &scopes); err != nil {
		t.Fatal(err)
	}
	if cardID != nil || strings.Join(scopes, ",") != "report:write,card:create" {
		t.Fatalf("autoresearch token: card %v scopes %v", cardID, scopes)
	}
	if err := env.App.QueryRow(ctx, "SELECT card_id, scopes FROM agent_run_token_lookup('hr')").Scan(&cardID, &scopes); err != nil {
		t.Fatal(err)
	}
	if cardID == nil || *cardID != card || strings.Join(scopes, ",") != "card:role" {
		t.Fatalf("role token: card %v scopes %v", cardID, scopes)
	}
	for _, h := range []string{"hs", "hw"} {
		var n int
		if err := env.App.QueryRow(ctx, "SELECT count(*) FROM agent_run_token_lookup($1)", h).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("token %s matched a card-less run it does not fit", h)
		}
	}
	// a card-less token goes with its run
	if _, err := env.Owner.Exec(ctx, "DELETE FROM agent_run WHERE id = $1", ap); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_token WHERE agent_run_id = $1", ap).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d card-less tokens outlived their run", left)
	}
}

func TestAutoresearchFunctionsRunAsTheApp(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, _ := seedWorkspace(t, env)
	trigger := func(status string) uuid.UUID {
		var ap, tr uuid.UUID
		if err := env.Owner.QueryRow(ctx, `INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode, status)
			VALUES ($1, $2, 'a', 'create_card', $3) RETURNING id`, ws, u, status).Scan(&ap); err != nil {
			t.Fatal(err)
		}
		if err := env.Owner.QueryRow(ctx, `INSERT INTO autoresearch_trigger (id, workspace_id, autoresearch_id, kind, cron, next_run_at, created_by)
			VALUES (gen_random_uuid(), $1, $2, 'schedule', '0 * * * *', now() - interval '1 minute', $3) RETURNING id`, ws, ap, u).Scan(&tr); err != nil {
			t.Fatal(err)
		}
		return tr
	}
	active, paused := trigger("active"), trigger("paused")
	rows, err := env.App.Query(ctx, "SELECT trigger_id FROM autoresearch_due(now())")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	due := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		due[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !due[active] || due[paused] || len(due) != 1 {
		t.Fatalf("due triggers: %v", due)
	}
	for _, q := range []string{
		"SELECT * FROM autoresearch_stale_runs(now())",
		"SELECT * FROM autoresearch_drift()",
		"SELECT * FROM autoresearch_webhook_lookup('nope')",
		"SELECT * FROM claim_webhook_deliveries(1)",
	} {
		if _, err := env.App.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestRunKeysAndInboxKinds(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, _ := seedWorkspace(t, env)
	var ap uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode) VALUES ($1, $2, 'a', 'run_only') RETURNING id`, ws, u).Scan(&ap); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO autoresearch_run (workspace_id, autoresearch_id, source, acting_user_id, planned_at, idempotency_key)
		VALUES ($1, $2, 'manual', $3, now(), 'k') RETURNING id`
	var run uuid.UUID
	if err := env.Owner.QueryRow(ctx, ins, ws, ap, u).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, ins, ws, ap, u); err == nil {
		t.Fatal("a second run with the same idempotency key was stored")
	}
	inbox := `INSERT INTO inbox_item (workspace_id, user_id, kind, text, autoresearch_run_id) VALUES ($1, $2, $3, 'r', $4)`
	if _, err := env.Owner.Exec(ctx, inbox, ws, u, "autoresearch_report", run); err != nil {
		t.Fatalf("a card-less report item: %v", err)
	}
	if _, err := env.Owner.Exec(ctx, inbox, ws, u, "nonsense", run); err == nil {
		t.Fatal("an unknown Inbox kind was stored")
	}
}
