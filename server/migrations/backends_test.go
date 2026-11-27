package migrations_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
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
