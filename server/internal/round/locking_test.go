package round

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/sources"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Wait for PostgreSQL's actual lock wait, not for a guessed scheduling delay.
func waitBlockedBy(t *testing.T, f *fix, ctx context.Context, pid uint32, done <-chan error) {
	t.Helper()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		if err := f.env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("operation completed before barrier: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
}

func TestBoardLockOrderAgainstClaimAndSteering(t *testing.T) {
	for _, scenario := range []string{"claim event FK", "claim queued run budget", "WIP versus steering", "workspace parent before card"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			col := board.Grounding
			if scenario == "WIP versus steering" {
				col = board.Backlog
			}
			card := f.card(t, col, 1)
			if scenario == "claim queued run budget" {
				if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.env.Owner.Exec(t.Context(), `UPDATE card SET budget_usd=1,spent_usd=1 WHERE id=$1`, card); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			first, err := f.env.Owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Rollback(context.Background())
			q := db.New(first)
			if scenario == "claim event FK" || scenario == "claim queued run budget" {
				if _, err = q.LockWorkspaceExecution(ctx, f.ws); err != nil {
					t.Fatal(err)
				}
				if err = q.LockWorkspaceEvents(ctx, f.ws); err != nil {
					t.Fatal(err)
				}
				if scenario == "claim queued run budget" {
					if _, err = first.Exec(ctx, `SELECT id FROM agent_run WHERE card_id=$1 FOR UPDATE`, card); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if _, err = q.LockWorkspaceForMutation(ctx, f.ws); err != nil {
					t.Fatal(err)
				}
				if scenario == "WIP versus steering" {
					if _, err = q.LockCard(ctx, db.LockCardParams{ID: card, WorkspaceID: f.ws}); err != nil {
						t.Fatal(err)
					}
				}
			}
			done := make(chan error, 1)
			go func() {
				if scenario == "WIP versus steering" {
					done <- f.e.WIPPull(ctx, f.ws)
				} else {
					done <- f.e.Advance(ctx, f.ws, card)
				}
			}()
			waitBlockedBy(t, f, ctx, first.Conn().PgConn().PID(), done)
			if scenario == "workspace parent before card" {
				_, err = q.LockCard(ctx, db.LockCardParams{ID: card, WorkspaceID: f.ws})
			} else {
				err = q.LockWorkspaceEvents(ctx, f.ws)
				if err == nil {
					_, err = q.InsertEvent(ctx, db.InsertEventParams{WorkspaceID: f.ws, CardID: &card, Type: "barrier.claim_or_steer", ActorType: "system", Payload: []byte(`{}`)})
				}
			}
			if err != nil {
				t.Errorf("opposing mutation deadlocked: %v", err)
				_ = first.Rollback(context.Background())
			} else if err = first.Commit(ctx); err != nil {
				t.Error(err)
			}
			if err = <-done; err != nil {
				t.Errorf("board operation: %v", err)
			}
		})
	}
}

func TestCardLockAllowsForeignKeyReaders(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: card, WorkspaceID: f.ws}); err != nil {
			return err
		}
		other, err := f.env.Owner.Begin(ctx)
		if err != nil {
			return err
		}
		defer other.Rollback(context.Background())
		_, err = other.Exec(ctx, `INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,'fk.reader','system','{}')`, f.ws, card)
		if err != nil {
			return fmt.Errorf("card lock blocked FK reader: %w", err)
		}
		return other.Commit(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSourceRecordWaitsBeforeFinalDigestCommit(t *testing.T) {
	f := setup(t)
	card := f.atGate(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var p *pending
	if err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) (err error) { p, err = f.e.read(ctx, tx, card); return err }); err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("no pending gate")
	}
	role, token := f.recordRole(t, card)
	held := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	gateDone := make(chan error, 1)
	go func() {
		gateDone <- f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
			_, err := f.e.write(ctx, tx, p, &board.Gate{Column: board.Grounding, Round: 1, InputDigest: p.v.Digest(ground.ReviewKinds...), Verdict: board.CannotProceed, Reason: "tested gate"})
			if err != nil {
				return err
			}
			close(held)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-held:
	case err := <-gateDone:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	h := sources.Hosted{Store: f.e.Store, Blobs: f.e.Blobs}
	recordDone := make(chan error, 1)
	go func() {
		recordDone <- h.Record(ctx, role, token, "search", "", map[string]any{"database": "geo", "query": "late search"}, "")
	}()
	// The gate has verified its digest but has not committed. A source writer
	// must wait before INSERT, even though its FK reader can coexist with the card.
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		var query string
		err := f.env.Owner.QueryRow(ctx, `SELECT query FROM pg_stat_activity WHERE datname=current_database() AND usename='lykeion_app' AND wait_event_type='Lock' LIMIT 1`).Scan(&query)
		if err == nil {
			if !strings.Contains(query, "LockWorkspaceExecution") && !strings.Contains(query, "LockCard") {
				t.Fatalf("record entered write/event path after digest: %s", query)
			}
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case err := <-recordDone:
			t.Fatalf("record completed inside gate snapshot fence: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
	// Closing the release channel here allows the gate commit, then the record.
	close(release)
	if err := <-gateDone; err != nil {
		t.Fatal(err)
	}
	if err := <-recordDone; err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := f.env.Owner.QueryRow(ctx, `SELECT body->>'input_digest' FROM record WHERE card_id=$1 AND kind='gate'`, card).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest != p.v.Digest(ground.ReviewKinds...) {
		t.Fatal("gate digest changed")
	}
	if n := f.count(t, card, `SELECT count(*) FROM record WHERE card_id=$1 AND body->>'query'='late search'`); n != 1 {
		t.Fatalf("late source not committed after gate: %d", n)
	}
}

func (f *fix) recordRole(t *testing.T, card uuid.UUID) (middleware.Role, string) {
	t.Helper()
	run := uuid.New()
	token := "ltk_" + run.String()
	if _, err := f.env.Owner.Exec(t.Context(), `INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,attempt) VALUES($1,$2,$3,$4,'grounding',1,'data-scout','claude','p','running',1)`, run, f.ws, card, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(t.Context(), `INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,card_id,role,attempt,expires_at) VALUES($1,$2,$3,$4,'data-scout',1,now()+interval '1 hour')`, auth.Hash(token), f.ws, run, card); err != nil {
		t.Fatal(err)
	}
	return middleware.Role{AgentRunID: run, WorkspaceID: f.ws, CardID: card, Column: board.Grounding, Round: 1, Role: ground.DataScout}, token
}

func TestSourceRecordProtectsWorkspaceBeforeChildLocks(t *testing.T) {
	f := setup(t)
	card := f.card(t, board.Grounding, 1)
	role, token := f.recordRole(t, card)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	parent, err := f.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Rollback(context.Background())
	if _, err = parent.Exec(ctx, `SELECT id FROM workspace WHERE id=$1 FOR UPDATE`, f.ws); err != nil {
		t.Fatal(err)
	}
	h := sources.Hosted{Store: f.e.Store, Blobs: f.e.Blobs}
	done := make(chan error, 1)
	go func() {
		done <- h.Record(ctx, role, token, "search", "", map[string]any{"database": "geo", "query": "mouse"}, "")
	}()
	waitBlockedBy(t, f, ctx, parent.Conn().PgConn().PID(), done)
	// The waiting source must not own a run or card that the parent mutator needs.
	if _, err = parent.Exec(ctx, `SELECT id FROM card WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, card); err != nil {
		t.Fatal(err)
	}
	if _, err = parent.Exec(ctx, `SELECT id FROM agent_run WHERE id=$1 FOR UPDATE NOWAIT`, role.AgentRunID); err != nil {
		t.Fatal(err)
	}
	if err = parent.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
