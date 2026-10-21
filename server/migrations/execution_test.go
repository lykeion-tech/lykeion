package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func TestExecutionTokenIsBoundToCurrentAttemptAndTenant(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	user, ws, card, run := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO "user" (id, email) VALUES ($1, 'attempt@x')`, []any{user}},
		{`INSERT INTO workspace (id, slug, name) VALUES ($1, 'attempt', 'w')`, []any{ws}},
		{`INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding')`, []any{card, ws, user}},
		{`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
			VALUES ($1, $2, $3, $4, 'backlog', 1, 'reviewer', 'claude', 'p', 'running', 1)`, []any{run, ws, card, user}},
		{`INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
			VALUES ('attempt-one', $1, $2, $3, 'reviewer', 1, now() + interval '1 hour')`, []any{ws, run, card}},
	} {
		if _, err := env.Owner.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	var gotRun, gotWS, gotCard uuid.UUID
	var role, column string
	var round int32
	if err := env.App.QueryRow(ctx, `SELECT agent_run_id, workspace_id, card_id, role, board_column, round
		FROM agent_run_token_lookup('attempt-one')`).Scan(&gotRun, &gotWS, &gotCard, &role, &column, &round); err != nil {
		t.Fatalf("lookup live token: %v", err)
	}
	if gotRun != run || gotWS != ws || gotCard != card || role != "reviewer" || column != "backlog" || round != 1 {
		t.Fatalf("lookup returned unexpected run identity: %v %v %v %q %q %d", gotRun, gotWS, gotCard, role, column, round)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET attempt = 2 WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM agent_run_token_lookup('attempt-one')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("previous attempt token remained valid: %d rows", count)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
		VALUES ('expired-token', $1, $2, $3, 'reviewer', 2, now() - interval '1 minute')`, ws, run, card); err != nil {
		t.Fatal(err)
	}
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM agent_run_token_lookup('expired-token')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("lookup accepted an expired token for a running attempt: %d rows", count)
	}
	otherWS, otherCard := uuid.New(), uuid.New()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspace (id, slug, name) VALUES ($1, 'other', 'w')`, []any{otherWS}},
		{`INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding')`, []any{otherCard, otherWS, user}},
	} {
		if _, err := env.Owner.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
		VALUES ('cross-tenant', $1, $2, $3, 'reviewer', 2, now() + interval '1 hour')`, otherWS, run, otherCard); err == nil {
		t.Fatal("accepted a token whose tenant and card do not match its run")
	}
}

func TestExecutionMessagesAndUsageAreAttemptScoped(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	user, ws, card, run := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO "user" (id, email) VALUES ($1, 'usage@x')`, []any{user}},
		{`INSERT INTO workspace (id, slug, name) VALUES ($1, 'usage', 'w')`, []any{ws}},
		{`INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding')`, []any{card, ws, user}},
		{`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
			VALUES ($1, $2, $3, $4, 'backlog', 1, 'reviewer', 'claude', 'p')`, []any{run, ws, card, user}},
	} {
		if _, err := env.Owner.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, attempt := range []int{1, 2} {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_message (workspace_id, agent_run_id, attempt, seq, type, content)
			VALUES ($1, $2, $3, 1, 'text', '{}')`, ws, run, attempt); err != nil {
			t.Fatalf("insert message for attempt %d: %v", attempt, err)
		}
		if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_usage (workspace_id, agent_run_id, attempt, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
			VALUES ($1, $2, $3, 'model-a', 1, 2, 0, 0, 0.01)`, ws, run, attempt); err != nil {
			t.Fatalf("insert usage for attempt %d: %v", attempt, err)
		}
	}
	var count int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run_message WHERE agent_run_id = $1 AND seq = 1`, run).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("messages with repeated sequence across attempts: %d, want 2", count)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run_usage WHERE agent_run_id = $1 AND model = 'model-a'`, run).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("usage rows across attempts: %d, want 2", count)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_usage (workspace_id, agent_run_id, attempt, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
		VALUES ($1, $2, 2, 'model-a', 3, 4, 0, 0, 0.02)`, ws, run); err == nil {
		t.Fatal("accepted duplicate model usage for the same attempt")
	}
}

func TestSweepFunctionsRunAsTheAppAndRecoverRuns(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	user, ws, card := uuid.New(), uuid.New(), uuid.New()
	runtimeStale, runtimeDeleted := uuid.New(), uuid.New()
	runStale, runDeleted, runExpired := uuid.New(), uuid.New(), uuid.New()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO "user" (id, email) VALUES ($1, 'sweep@x')`, []any{user}},
		{`INSERT INTO workspace (id, slug, name) VALUES ($1, 'sweep', 'w')`, []any{ws}},
		{`INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding')`, []any{card, ws, user}},
		{`INSERT INTO runtime (id, workspace_id, owner_id, daemon_id, harness, last_seen_at)
			VALUES ($1, $2, $3, 'stale', 'claude', now() - interval '5 minutes')`, []any{runtimeStale, ws, user}},
		{`INSERT INTO runtime (id, workspace_id, owner_id, daemon_id, harness)
			VALUES ($1, $2, $3, 'deleted', 'claude')`, []any{runtimeDeleted, ws, user}},
		{`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, runtime_id)
			VALUES ($1, $2, $3, $4, 'backlog', 1, 'reviewer', 'claude', 'p', 'running', 1, $5)`, []any{runStale, ws, card, user, runtimeStale}},
		{`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, runtime_id)
			VALUES ($1, $2, $3, $4, 'backlog', 1, 'reviewer', 'claude', 'p', 'running', 2, $5)`, []any{runDeleted, ws, card, user, runtimeDeleted}},
		{`INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, max_attempts, prepare_lease_expires_at)
			VALUES ($1, $2, $3, $4, 'backlog', 1, 'reviewer', 'claude', 'p', 'dispatched', 2, 2, now() - interval '1 minute')`, []any{runExpired, ws, card, user}},
		{`INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
			VALUES ('sweep-token', $1, $2, $3, 'reviewer', 1, now() + interval '1 hour')`, []any{ws, runStale, card}},
	} {
		if _, err := env.Owner.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.Owner.Exec(ctx, `DELETE FROM runtime WHERE id = $1`, runtimeDeleted); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := env.App.QueryRow(ctx, "SELECT sweep_runtimes('60 seconds')").Scan(&n); err != nil || n != 1 {
		t.Fatalf("sweep_runtimes as app: %d %v", n, err)
	}
	var state string
	var reason *string
	if err := env.Owner.QueryRow(ctx, `SELECT status, failure_reason FROM agent_run WHERE id = $1`, runStale).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "running" {
		t.Fatalf("runtime sweep unexpectedly changed run state: %s", state)
	}
	rows, err := env.App.Query(ctx, "SELECT agent_run_id, status FROM sweep_agent_runs()")
	if err != nil {
		t.Fatal(err)
	}
	swept := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		swept[id] = status
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(swept) != 3 || swept[runStale] != "queued" || swept[runDeleted] != "failed" || swept[runExpired] != "failed" {
		t.Fatalf("unexpected swept runs: %#v", swept)
	}
	for _, tc := range []struct {
		id     uuid.UUID
		status string
		reason string
	}{
		{runStale, "queued", "runtime_offline"},
		{runDeleted, "failed", "runtime_offline"},
		{runExpired, "failed", "timeout"},
	} {
		if err := env.Owner.QueryRow(ctx, `SELECT status, failure_reason FROM agent_run WHERE id = $1`, tc.id).Scan(&state, &reason); err != nil {
			t.Fatal(err)
		}
		if state != tc.status || reason == nil || *reason != tc.reason {
			t.Fatalf("run %s got %s/%v, want %s/%s", tc.id, state, reason, tc.status, tc.reason)
		}
	}
	var count int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM agent_run_token_lookup('sweep-token')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("sweep left a token active: %d rows", count)
	}
}
