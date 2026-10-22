package sources

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"lykeion/server/internal/store"
)

func TestRecordCallAuthorityFenceSharesEvidenceTransaction(t *testing.T) {
	for _, cardless := range []bool{false, true} {
		name := "card"
		if cardless {
			name = "cardless"
		}
		t.Run(name, func(t *testing.T) {
			f := newConnFix(t, ok("unused"))
			ctx := t.Context()
			card := f.card
			if cardless {
				card = uuid.Nil
			}
			role := f.role(card, "agent")
			body, rest := NewCall(role, "library", "echo", nil, strings.Repeat("x", MaxCallAnswer+1), false)
			rejected := errors.New("connector authority withdrawn")
			calls := 0
			check := func(tx *store.Tx) error {
				calls++
				competing, err := f.env.Owner.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer competing.Rollback(ctx)
				_, err = competing.Exec(ctx, `SELECT id FROM agent_run WHERE id=$1 FOR UPDATE NOWAIT`, role.AgentRunID)
				var pg *pgconn.PgError
				if !errors.As(err, &pg) || pg.Code != "55P03" {
					t.Fatalf("authority callback lacks run lock: %v", err)
				}
				if err := tx.Emit(ctx, "receipt.test", nil, store.System, map[string]any{}); err != nil {
					return err
				}
				return rejected
			}
			if _, err := f.h.RecordCall(ctx, role, runTok(role), body, rest, check); !errors.Is(err, rejected) {
				t.Fatalf("record ignored authority: %v", err)
			}
			if calls != 1 || f.count(`SELECT count(*) FROM record`) != 0 || f.count(`SELECT count(*) FROM event`) != 0 {
				t.Fatal("rejected fence persisted evidence or events")
			}
			if _, err := f.h.RecordCall(ctx, role, runTok(role), body, rest); err != nil {
				t.Fatal(err)
			}
			if cardless {
				if f.count(`SELECT count(*) FROM record`) != 0 || f.count(`SELECT count(*) FROM event WHERE type='sources.called'`) != 1 {
					t.Fatal("cardless call behavior changed")
				}
			} else if f.count(`SELECT count(*) FROM record WHERE kind='call'`) != 1 || f.count(`SELECT count(*) FROM event WHERE type='blob.created'`) != 1 {
				t.Fatal("ordinary call/blob behavior changed")
			}
		})
	}
}

func TestOnlyTheServerRecordsAnAttachment(t *testing.T) {
	f := newConnFix(t, ok("unused"))
	role := f.role(f.card, "agent")
	for _, server := range []string{"attachments", " Attachments ", "ATTACHMENTS\t"} {
		body, rest := NewCall(role, server, "attach", nil, "PGC1A,9.9", false)
		if _, err := f.h.RecordCall(t.Context(), role, runTok(role), body, rest); !errors.Is(err, ErrReserved) {
			t.Fatalf("%q: %v", server, err)
		}
	}
	if n := f.count(`SELECT count(*) FROM record`); n != 0 {
		t.Fatalf("records: %d", n)
	}
	body, rest := NewCall(role, "attachments-lab", "attach", nil, "PGC1A,9.9", false)
	if _, err := f.h.RecordCall(t.Context(), role, runTok(role), body, rest); err != nil {
		t.Fatalf("a name that only starts like it: %v", err)
	}
}
