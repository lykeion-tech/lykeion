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

// Stored rows retain legacy harnesses so an upgrade can explain why they cannot run.
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
	stored := map[string]bool{"cursor": true, "antigravity": true}
	for h := range agent.Harnesses {
		stored[h] = true
	}
	for table, query := range queries {
		t.Run(table, func(t *testing.T) {
			for h := range stored {
				args := []any{ws, u, h}
				if table == "agent_run" {
					args = append(args, card)
				}
				if _, err := env.Owner.Exec(ctx, query, args...); err != nil {
					t.Errorf("%s %s: %v", table, h, err)
				}

				var readable bool
				if err := env.Owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE workspace_id=$1 AND harness=$2)`, ws, h).Scan(&readable); err != nil {
					t.Fatal(err)
				}
				if !readable {
					t.Errorf("%s %s row is unreadable", table, h)
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
			if len(names) != len(stored) {
				t.Errorf("CHECK differs from supported and historical stored harnesses: %s", definition)
			}
			for _, name := range names {
				if !stored[name[1]] {
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
	var fetchID uuid.UUID
	claim := func() int64 {
		id := uuid.New()
		n, err := q.ClaimInputFetch(ctx, db.ClaimInputFetchParams{ID: ar, WorkspaceID: ws, FetchID: id, Lease: lease})
		if err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			fetchID = id
		}
		return n
	}
	renew := func() int64 {
		n, err := q.RenewInputFetch(ctx, db.RenewInputFetchParams{ID: ar, WorkspaceID: ws, FetchID: fetchID, Lease: lease})
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
	if n, err := q.ReleaseInputFetch(ctx, db.ReleaseInputFetchParams{ID: ar, WorkspaceID: ws, FetchID: fetchID}); err != nil || n != 1 || claim() != 1 {
		t.Fatalf("a released claim was not free: %v", err)
	}
	if err := q.RenewInputLease(ctx, db.RenewInputLeaseParams{ID: ar, WorkspaceID: ws, Lease: lease}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{`{"a": {"failed": "gone"}}`, `{"b": {"input": {}}}`} {
		if n, err := q.RecordInputOutcome(ctx, db.RecordInputOutcomeParams{ID: ar, WorkspaceID: ws, FetchID: fetchID, Outcome: []byte(o)}); err != nil || n != 1 {
			t.Fatalf("record outcome: rows=%d error=%v", n, err)
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
	if err := env.Owner.QueryRow(ctx, `SELECT input_outcomes = '{}' AND input_lease_expires_at IS NULL AND input_fetch_id IS NULL AND input_fetch_expires_at IS NULL FROM agent_run WHERE id = $1`, ar).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("clearing left outcomes or the lease")
	}
}

func TestInputFetchTakeoverIsFenced(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws := uuid.New(), uuid.New()
	var card, ar uuid.UUID
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO "user"(id,email) VALUES($1,'fetch@lab.org')`, u)
	must(`INSERT INTO workspace(id,slug,name) VALUES($1,'fetch','Fetch')`, ws)
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card(workspace_id,number,title,question,owner_id) VALUES($1,1,'t','q',$2) RETURNING id`, ws, u).Scan(&card); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run(workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt) VALUES($1,$2,$3,'grounding',1,'scoper','claude','p') RETURNING id`, ws, card, u).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	q := db.New(env.Owner)
	lease := pgtype.Interval{Microseconds: 60e6, Valid: true}
	rows := func(label string, want, n int64, err error) {
		t.Helper()
		if err != nil || n != want {
			t.Fatalf("%s: rows=%d want=%d error=%v", label, n, want, err)
		}
	}
	claim := func(owner, workspace uuid.UUID, want int64) {
		t.Helper()
		n, err := q.ClaimInputFetch(ctx, db.ClaimInputFetchParams{ID: ar, WorkspaceID: workspace, FetchID: owner, Lease: lease})
		rows("claim", want, n, err)
	}
	renew := func(owner, workspace uuid.UUID, want int64) {
		t.Helper()
		n, err := q.RenewInputFetch(ctx, db.RenewInputFetchParams{ID: ar, WorkspaceID: workspace, FetchID: owner, Lease: lease})
		rows("renew", want, n, err)
	}
	record := func(owner, workspace uuid.UUID, want int64, outcome string) {
		t.Helper()
		n, err := q.RecordInputOutcome(ctx, db.RecordInputOutcomeParams{ID: ar, WorkspaceID: workspace, FetchID: owner, Outcome: []byte(outcome)})
		rows("outcome", want, n, err)
	}
	release := func(owner, workspace uuid.UUID, want int64) {
		t.Helper()
		n, err := q.ReleaseInputFetch(ctx, db.ReleaseInputFetchParams{ID: ar, WorkspaceID: workspace, FetchID: owner})
		rows("release", want, n, err)
	}
	daemonLease := func(workspace uuid.UUID) {
		t.Helper()
		if err := q.RenewInputLease(ctx, db.RenewInputLeaseParams{ID: ar, WorkspaceID: workspace, Lease: lease}); err != nil {
			t.Fatal(err)
		}
	}
	clear := func(workspace uuid.UUID) {
		t.Helper()
		if err := q.ClearInputFetch(ctx, db.ClearInputFetchParams{ID: ar, WorkspaceID: workspace}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var state string
		if err := env.Owner.QueryRow(ctx, `SELECT jsonb_build_object('owner',input_fetch_id,'fetch',input_fetch_expires_at,'lease',input_lease_expires_at,'outcomes',input_outcomes)::text FROM agent_run WHERE id=$1`, ar).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	unchanged := func(before string) {
		t.Helper()
		if after := snapshot(); after != before {
			t.Fatalf("claim changed:\nbefore %s\nafter  %s", before, after)
		}
	}
	rejectedWorker := func(owner, workspace uuid.UUID) {
		t.Helper()
		before := snapshot()
		renew(owner, workspace, 0)
		unchanged(before)
		record(owner, workspace, 0, `{"stale":{"failed":"A"}}`)
		unchanged(before)
		release(owner, workspace, 0)
		unchanged(before)
	}
	assertOwner := func(owner uuid.UUID) {
		t.Helper()
		var got uuid.UUID
		if err := env.Owner.QueryRow(ctx, `SELECT input_fetch_id FROM agent_run WHERE id=$1`, ar).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != owner {
			t.Fatalf("fetch owner=%s want=%s", got, owner)
		}
	}
	assertReleased := func() {
		t.Helper()
		var released bool
		if err := env.Owner.QueryRow(ctx, `SELECT input_fetch_id IS NULL AND input_fetch_expires_at IS NULL FROM agent_run WHERE id=$1`, ar).Scan(&released); err != nil {
			t.Fatal(err)
		}
		if !released {
			t.Fatal("release retained a fetch identity or expiry")
		}
	}
	a, b := uuid.New(), uuid.New()
	daemonLease(ws)
	claim(a, ws, 1)
	assertOwner(a)
	before := snapshot()
	claim(b, ws, 0)
	unchanged(before)
	must(`UPDATE agent_run SET input_fetch_expires_at=now()-interval '1 second' WHERE id=$1`, ar)
	claim(b, ws, 1)
	assertOwner(b)
	rejectedWorker(a, ws)
	renew(b, ws, 1)
	record(b, ws, 1, `{"current":{"input":{}}}`)
	var recorded bool
	if err := env.Owner.QueryRow(ctx, `SELECT input_outcomes ? 'current' FROM agent_run WHERE id=$1`, ar).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if !recorded {
		t.Fatal("current owner failed to persist its outcome")
	}

	// A different tenant cannot mutate a live claim, including clearing it.
	other := uuid.New()
	rejectedWorker(b, other)
	before = snapshot()
	daemonLease(other)
	clear(other)
	unchanged(before)
	release(b, ws, 1)
	assertReleased()
	release(b, ws, 0)
	before = snapshot()
	claim(uuid.New(), other, 0)
	unchanged(before)

	// Even without takeover, an expired fetch cannot revive or write outcomes.
	c := uuid.New()
	claim(c, ws, 1)
	must(`UPDATE agent_run SET input_fetch_expires_at=now()-interval '1 second' WHERE id=$1`, ar)
	before = snapshot()
	renew(c, ws, 0)
	record(c, ws, 0, `{"expired":{"failed":"late"}}`)
	unchanged(before)
	release(c, ws, 1)
	assertReleased()

	// Clearing invalidates a worker and resets both leases and outcomes.
	d := uuid.New()
	claim(d, ws, 1)
	clear(ws)
	var cleared bool
	if err := env.Owner.QueryRow(ctx, `SELECT input_fetch_id IS NULL AND input_fetch_expires_at IS NULL AND input_lease_expires_at IS NULL AND input_outcomes='{}' FROM agent_run WHERE id=$1`, ar).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("clear retained fetch ownership, leases or outcomes")
	}
	daemonLease(ws)
	rejectedWorker(d, ws)
	e := uuid.New()
	claim(e, ws, 1)
	rejectedWorker(d, ws)
	assertOwner(e)

	// A current fetch owner must also stop when the daemon input lease lapses.
	must(`UPDATE agent_run SET input_lease_expires_at=now()-interval '1 second' WHERE id=$1`, ar)
	before = snapshot()
	renew(e, ws, 0)
	record(e, ws, 0, `{"expired-daemon":{"failed":"late"}}`)
	unchanged(before)
	release(e, ws, 1)
	assertReleased()
}
