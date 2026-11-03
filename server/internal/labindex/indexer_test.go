package labindex

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/embed"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

type failingEmbedder struct{}

func (failingEmbedder) Embed(context.Context, []string, bool) ([][]float32, error) {
	return nil, errors.New("TEI unavailable")
}

func indexerFixture(t *testing.T) (*Indexer, *testutil.Env, uuid.UUID, uuid.UUID) {
	t.Helper()
	if os.Getenv("LYK_INDEXER_INTEGRATION") != "1" {
		t.Skip("set LYK_INDEXER_INTEGRATION=1 with isolated test services")
	}
	env := testutil.Setup(t)
	ws, card, user := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email,name) VALUES($1,'indexer@example.org','Indexer')`, []any{user}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'indexer','Indexer')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, []any{ws, user}},
		{`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'Study','Question',$3,'grounding')`, []any{card, ws, user}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return &Indexer{Store: &store.Store{Pool: env.App}, Index: New(env.Redis, embed.Fake{}, embed.Model)}, env, ws, card
}
func emitIndexEvent(t *testing.T, x *Indexer, ws, card uuid.UUID, typ string) {
	t.Helper()
	if err := x.Store.InWorkspace(context.Background(), ws, uuid.Nil, func(tx *store.Tx) error { return tx.Emit(context.Background(), typ, &card, store.System, nil) }); err != nil {
		t.Fatal(err)
	}
}
func indexCursor(t *testing.T, env *testutil.Env) int64 {
	t.Helper()
	var n int64
	if err := env.Owner.QueryRow(context.Background(), `SELECT COALESCE((SELECT last_event_id FROM lab_cursor WHERE consumer='indexer'),0)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestIndexerIndexesNewRecordAndAdvances(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'grounding',1,'paper','grounding','{"text":"Evidence"}')`, id, ws, card); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "record.created")
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "record", ID: id})).Val() != 1 || indexCursor(t, env) == 0 {
		t.Fatal("record missing or cursor not advanced")
	}
	// A changed model must backfill even after the durable cursor is exhausted.
	x.Index.model = "new-model"
	x.initialized = false
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := env.Redis.HGet(ctx, key(ws, labmemory.Ref{Kind: "record", ID: id}), "model").Val(); got != "new-model" {
		t.Fatal(got)
	}
}
func TestIndexerDeletesCancelledComment(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	id := uuid.New()
	var user uuid.UUID
	if err := env.Owner.QueryRow(ctx, `SELECT owner_id FROM card WHERE id=$1`, card).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO comment(id,workspace_id,card_id,author_id,body) VALUES($1,$2,$3,$4,'Discussion')`, id, ws, card, user); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "comment.created")
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	k := key(ws, labmemory.Ref{Kind: "comment", ID: id})
	if env.Redis.Exists(ctx, k).Val() != 1 {
		t.Fatal("comment missing")
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO comment_cancellation(workspace_id,comment_id,author_id) VALUES($1,$2,$3)`, ws, id, user); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "comment.cancelled")
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, k).Val() != 0 {
		t.Fatal("cancelled comment retained")
	}
}
func TestIndexerKeepsCursorWhenEmbedderFails(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	// Finish startup backfill, then fail on a changed study projection.
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET title='Changed',version=version+1 WHERE id=$1`, card); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "card.steered")
	before := indexCursor(t, env)
	x.Index.e = failingEmbedder{}
	if err := x.Tick(ctx); err == nil {
		t.Fatal("expected embedding failure")
	}
	if indexCursor(t, env) != before {
		t.Fatal("cursor advanced on failure")
	}
	x.Index.e = embed.Fake{}
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if indexCursor(t, env) <= before {
		t.Fatal("retry did not advance")
	}
}
func TestIndexerSkipsCoresearchEvents(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	keys := env.Redis.DBSize(ctx).Val()
	x.Index.e = failingEmbedder{}
	emitIndexEvent(t, x, ws, card, "coresearch.message.created")
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if env.Redis.DBSize(ctx).Val() != keys || indexCursor(t, env) == 0 {
		t.Fatal("Coresearch event indexed or not acknowledged")
	}
}
func TestIndexerEventCoverage(t *testing.T) {
	for _, s := range []string{"record.created", "card.deleted", "comment.cancelled", "inbox.created", "team.updated", "workspace.updated", "attachment.created", "agent_run.settled", "lesson.updated"} {
		if !relevantEvent(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"coresearch.message.created", "lab.message.created", "answer.created", "chat.created"} {
		if relevantEvent(s) {
			t.Fatal(s)
		}
	}
}

func TestIndexerDeletesStudyDescendantsWithoutPayloadIDs(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'grounding',1,'paper','grounding','{}')`, id, ws, card); err != nil {
		t.Fatal(err)
	}
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `DELETE FROM card WHERE id=$1`, card); err != nil {
		t.Fatal(err)
	}
	// Real deleted events have nil CardID; reconciliation must not rely on it.
	if err := x.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		return tx.Emit(ctx, "card.deleted", nil, store.System, map[string]any{"card_id": card})
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range []labmemory.Ref{{Kind: "study", ID: card}, {Kind: "record", ID: id}} {
		if env.Redis.Exists(ctx, key(ws, r)).Val() != 0 {
			t.Fatalf("retained %v", r)
		}
	}
}

func TestIndexerIndexesDecisionWithOnlyCardID(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var user uuid.UUID
	if err := env.Owner.QueryRow(ctx, `SELECT owner_id FROM card WHERE id=$1`, card).Scan(&user); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,options) VALUES($1,$2,$3,$4,'agent_decision','Question','[{"id":"a"}]')`, id, ws, card, user); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "inbox.created")
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "decision", ID: id})).Val() != 1 {
		t.Fatal("decision missing")
	}
}
