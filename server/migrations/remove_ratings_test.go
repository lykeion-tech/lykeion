package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestRemoveRatingsPreservesStudyAndRunHistory(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	read := func(name string) string {
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	exec := func(sql string, args ...any) {
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(read("0076_remove_ratings_and_preserve_run_history.down.sql"))
	user, ws, card := seedWorkspace(t, env)
	var pack uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO field_pack (workspace_id,name,version,sha256,manifest,blob_key,source) VALUES ($1,'Team','1','hash','{}','key','shipped') RETURNING id`, ws).Scan(&pack); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at, rating) VALUES ($1,$2,'grounding',$3,$4,true,now(),3)`, ws, card, pack, user)
	var legacy, queued, cancelled, retried uuid.UUID
	insertRun := `INSERT INTO agent_run (workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,completed_at) VALUES ($1,$2,$3,'grounding',1,$4,'claude','', $5, CASE WHEN $5='completed' THEN now() ELSE NULL END) RETURNING id`
	if err := env.Owner.QueryRow(ctx, insertRun, ws, card, user, "legacy", "completed").Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, insertRun, ws, card, user, "queued", "queued").Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, insertRun, ws, card, user, "cancelled", "cancelled").Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE agent_run SET completed_at=now() WHERE id=$1`, cancelled)
	if err := env.Owner.QueryRow(ctx, insertRun, ws, card, user, "retried", "queued").Scan(&retried); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES ($1,$2,'agent_run.started','system',jsonb_build_object('agent_run_id',$3::text))`, ws, card, retried)
	exec(read("0076_remove_ratings_and_preserve_run_history.up.sql"))
	var tableExists, cardExists, oldMarked, newMarked, cancelledMarked, retryStarted bool
	if err := env.Owner.QueryRow(ctx, `SELECT to_regclass('public.team_rating') IS NOT NULL, EXISTS(SELECT 1 FROM card WHERE id=$1), (SELECT legacy_unverified_started FROM agent_run WHERE id=$2), (SELECT legacy_unverified_started FROM agent_run WHERE id=$3), (SELECT legacy_unverified_started FROM agent_run WHERE id=$4), (SELECT first_started_at IS NOT NULL FROM agent_run WHERE id=$5)`, card, legacy, queued, cancelled, retried).Scan(&tableExists, &cardExists, &oldMarked, &newMarked, &cancelledMarked, &retryStarted); err != nil {
		t.Fatal(err)
	}
	if tableExists || !cardExists || !oldMarked || newMarked || cancelledMarked || !retryStarted {
		t.Fatalf("ratings %v card %v legacy %v queued %v cancelled %v retry started %v", tableExists, cardExists, oldMarked, newMarked, cancelledMarked, retryStarted)
	}
}
