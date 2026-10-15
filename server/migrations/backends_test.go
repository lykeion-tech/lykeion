package migrations_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// The harness CHECKs must accept exactly the harnesses the daemon can run.
func TestEveryHarnessIsStorable(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO "user" (id,email) VALUES ($1,'u@x')`, u)
	must(`INSERT INTO workspace (id,slug,name) VALUES ($1,'s','w')`, ws)
	var card uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id,number,title,question,owner_id,board_column,round) VALUES ($1,1,'t','q',$2,'grounding',1) RETURNING id`, ws, u).Scan(&card); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{
		"runtime":   `INSERT INTO runtime (workspace_id,owner_id,daemon_id,harness) VALUES ($1,$2,$3,$3)`,
		"agent_run": `INSERT INTO agent_run (workspace_id,responsible_user_id,harness,card_id,board_column,round,role,prompt) VALUES ($1,$2,$3,$4,'grounding',1,'scoper','p')`,
		"agent":     `INSERT INTO agent (workspace_id,owner_id,harness,name) VALUES ($1,$2,$3,$3)`,
	}
	for table, query := range queries {
		t.Run(table, func(t *testing.T) {
			for h := range agent.Harnesses {
				args := []any{ws, u, h}
				if table == "agent_run" {
					args = append(args, card)
				}
				if _, err := env.Owner.Exec(ctx, query, args...); err != nil {
					t.Errorf("%s %s: %v", table, h, err)
				}
			}
			args := []any{ws, u, "gemini"}
			if table == "agent_run" {
				args = append(args, card)
			}
			_, err := env.Owner.Exec(ctx, query, args...)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != table+"_harness_check" {
				t.Errorf("unknown %s harness: %v", table, err)
			}
			var definition string
			if err := env.Owner.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=$1`, table+"_harness_check").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			names := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(definition, -1)
			if len(names) != len(agent.Harnesses) {
				t.Errorf("CHECK differs from Harnesses: %s", definition)
			}
			for _, name := range names {
				if _, ok := agent.Harnesses[name[1]]; !ok {
					t.Errorf("CHECK contains unknown harness %s", name[1])
				}
			}
		})
	}
}

// One server at a time holds an agent run's input fetch, it renews the claim
// only while a daemon still asks, and each file's outcome stays until cleared.
func TestInputFetchLease(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	var card, ar uuid.UUID
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, round)
		VALUES ($1, 1, 't', 'q', $2, 'grounding', 1) RETURNING id`, ws, u).Scan(&card); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p') RETURNING id`, ws, card, u).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	q := db.New(env.Owner)
	lease := pgtype.Interval{Microseconds: 60e6, Valid: true}
	claim := func() int64 {
		n, err := q.ClaimInputFetch(ctx, db.ClaimInputFetchParams{ID: ar, WorkspaceID: ws, Lease: lease})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	renew := func() int64 {
		n, err := q.RenewInputFetch(ctx, db.RenewInputFetchParams{ID: ar, WorkspaceID: ws, Lease: lease})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if claim() != 1 || claim() != 0 {
		t.Fatal("a fetch claim must go to exactly one caller")
	}
	if renew() != 0 {
		t.Fatal("a fetch renewed its claim with no daemon asking")
	}
	if err := q.RenewInputLease(ctx, db.RenewInputLeaseParams{ID: ar, WorkspaceID: ws, Lease: lease}); err != nil || renew() != 1 {
		t.Fatalf("a fetch could not renew while a daemon asks: %v", err)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET input_lease_expires_at = now() - interval '1 second' WHERE id = $1`, ar); err != nil {
		t.Fatal(err)
	}
	if renew() != 0 {
		t.Fatal("a fetch renewed its claim after the input lease lapsed")
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET input_fetch_expires_at = now() - interval '1 second' WHERE id = $1`, ar); err != nil {
		t.Fatal(err)
	}
	if claim() != 1 {
		t.Fatal("a lapsed claim was not taken over")
	}
	if err := q.ReleaseInputFetch(ctx, db.ReleaseInputFetchParams{ID: ar, WorkspaceID: ws}); err != nil || claim() != 1 {
		t.Fatalf("a released claim was not free: %v", err)
	}
	for _, o := range []string{`{"a": {"failed": "gone"}}`, `{"b": {"input": {}}}`} {
		if err := q.RecordInputOutcome(ctx, db.RecordInputOutcomeParams{ID: ar, WorkspaceID: ws, Outcome: []byte(o)}); err != nil {
			t.Fatal(err)
		}
	}
	var both, cleared bool
	if err := env.Owner.QueryRow(ctx, `SELECT input_outcomes->'a'->>'failed' = 'gone' AND input_outcomes->'b' IS NOT NULL FROM agent_run WHERE id = $1`, ar).Scan(&both); err != nil {
		t.Fatal(err)
	}
	if !both {
		t.Fatal("an outcome replaced another file's")
	}
	if err := q.ClearInputFetch(ctx, db.ClearInputFetchParams{ID: ar, WorkspaceID: ws}); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT input_outcomes = '{}' AND input_lease_expires_at IS NULL FROM agent_run WHERE id = $1`, ar).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("clearing left outcomes or the lease")
	}
}
