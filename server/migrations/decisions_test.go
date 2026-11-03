package migrations_test

import (
	"io/fs"
	"testing"
	"time"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestDecisionColumns(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	ins := `INSERT INTO inbox_item (workspace_id, user_id, card_id, kind, text, decision_kind) VALUES ($1, $2, $3, $4, 't', $5)`

	if _, err := env.Owner.Exec(ctx, ins, ws, u, card, "agent_decision", "scope"); err != nil {
		t.Fatal(err)
	}
	_, err := env.Owner.Exec(ctx, ins, ws, u, card, "agent_decision", "cost")
	wantCode(t, err, "23505", "second open agent decision on one study")
	_, err = env.Owner.Exec(ctx, ins, ws, u, card, "machine_offline", "method")
	wantCode(t, err, "23514", "unknown decision_kind")
	_, err = env.Owner.Exec(ctx, ins, ws, u, card, "escalated", "")
	wantCode(t, err, "23514", "escalated kind")
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET state = 'blocked', block_kind = 'parked' WHERE id = $1`, card); err != nil {
		t.Fatal(err)
	}
}

func TestOpenStopsGetOptions(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	up, err := fs.ReadFile(migrations.FS, "0048_decisions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := fs.ReadFile(migrations.FS, "0048_decisions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	u, ws, card := seedWorkspace(t, env)
	ins := `INSERT INTO inbox_item (workspace_id, user_id, card_id, kind, text, cause, resolved_at) VALUES ($1, $2, $3, $4, 't', $5, $6) RETURNING id`
	add := func(kind, cause string, resolved *time.Time) (id string) {
		t.Helper()
		var c *string
		if cause != "" {
			c = &cause
		}
		if err := env.Owner.QueryRow(ctx, ins, ws, u, card, kind, c, resolved).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now()
	stop := add("needs_decision", "could_not_do", nil)
	blocked := add("blocked", "", nil)
	machine := add("needs_decision", "machine_failures", nil)
	resolved := add("blocked", "", &now)
	readDone := add("done", "", &now)
	wait := add("waiting_for_machine", "", &now)
	info := add("done", "", nil)

	if _, err := env.Owner.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{stop, blocked, machine} {
		var def, status string
		var in7 bool
		var ids []string
		if err := env.Owner.QueryRow(ctx, `SELECT default_option, status, abs(extract(epoch FROM expires_at - now() - interval '7 days')) < 60,
			(SELECT array_agg(o->>'id') FROM jsonb_array_elements(options) o) FROM inbox_item WHERE id = $1`, id).Scan(&def, &status, &in7, &ids); err != nil {
			t.Fatal(err)
		}
		want := []string{"redo", "close", "park"}
		if id == machine {
			want = []string{"try_again", "park"}
		}
		if def != "park" || status != "open" || !in7 || len(ids) != len(want) {
			t.Fatalf("%s: default %q status %q in7 %v options %v", id, def, status, in7, ids)
		}
		for i := range want {
			if ids[i] != want[i] {
				t.Fatalf("%s: options %v, want %v", id, ids, want)
			}
		}
	}
	// Only a resolved stop was a choice someone made; every other resolved
	// item was dismissed.
	for id, want := range map[string]string{resolved: "decided", readDone: "dismissed", wait: "dismissed"} {
		var status string
		if err := env.Owner.QueryRow(ctx, `SELECT status FROM inbox_item WHERE id = $1`, id).Scan(&status); err != nil || status != want {
			t.Fatalf("resolved item %s: status %q, want %q, %v", id, status, want, err)
		}
	}
	var open bool
	if err := env.Owner.QueryRow(ctx, `SELECT expires_at IS NULL AND status = 'open' FROM inbox_item WHERE id = $1`, info).Scan(&open); err != nil || !open {
		t.Fatalf("informational item should stay open without expiry: %v", err)
	}
}

func TestDecisionReferencesClearOnDelete(t *testing.T) {
	env := testutil.Setup(t)
	for _, col := range []string{"decided_by", "raised_run_id"} {
		var action string
		err := env.Owner.QueryRow(t.Context(), `SELECT c.confdeltype::text FROM pg_constraint c
			JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
			WHERE c.conrelid = 'inbox_item'::regclass AND c.contype = 'f' AND a.attname = $1`, col).Scan(&action)
		if err != nil || action != "n" {
			t.Errorf("%s: on delete %q, want set null (n), %v", col, action, err)
		}
	}
}
