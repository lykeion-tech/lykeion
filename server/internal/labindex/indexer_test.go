package labindex

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"

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

// Drain bounded ticks for integration assertions; production calls once per interval.
func drainIndexer(ctx context.Context, x *Indexer) error {
	for n := 0; n < 2000; n++ {
		if err := x.Tick(ctx); err != nil {
			return err
		}
		var pending int
		if err := x.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM lab_pending_events('indexer',200)`).Scan(&pending); err != nil {
			return err
		}
		if x.initialized && x.work == nil && pending == 0 {
			return nil
		}
	}
	return errors.New("indexer did not drain")
}

func acknowledgedCount(t *testing.T, env *testutil.Env) int64 {
	t.Helper()
	var n int64
	if err := env.Owner.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM event)-(SELECT count(*) FROM lab_event_delivery WHERE consumer='indexer')`).Scan(&n); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "record", ID: id})).Val() != 1 || acknowledgedCount(t, env) == 0 {
		t.Fatal("record missing or cursor not advanced")
	}
	// A changed model must backfill even after the durable cursor is exhausted.
	x.Index.model = "new-model"
	x.initialized = false
	if err := drainIndexer(ctx, x); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET title='Changed',version=version+1 WHERE id=$1`, card); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "card.steered")
	before := acknowledgedCount(t, env)
	x.Index.e = failingEmbedder{}
	if err := drainIndexer(ctx, x); err == nil {
		t.Fatal("expected embedding failure")
	}
	if acknowledgedCount(t, env) != before {
		t.Fatal("cursor advanced on failure")
	}
	x.Index.e = embed.Fake{}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if acknowledgedCount(t, env) <= before {
		t.Fatal("retry did not advance")
	}
}
func TestIndexerSkipsCoresearchEvents(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	keys := env.Redis.DBSize(ctx).Val()
	x.Index.e = failingEmbedder{}
	emitIndexEvent(t, x, ws, card, "coresearch.message.created")
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if env.Redis.DBSize(ctx).Val() != keys || acknowledgedCount(t, env) == 0 {
		t.Fatal("Coresearch event indexed or not acknowledged")
	}
}
func TestIndexerEventCoverage(t *testing.T) {
	for _, s := range []string{"record.created", "card.deleted", "comment.cancelled", "inbox.created", "team.updated", "workspace.updated", "attachment.created", "agent_run.settled", "lesson.updated", "decision.reminder", "study.files_attached", "stage_addon.updated"} {
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
	if err := drainIndexer(ctx, x); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
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
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "decision", ID: id})).Val() != 1 {
		t.Fatal("decision missing")
	}
}

func TestIndexerDeliversLowerIDCommittedAfterHigherID(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	wsB := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) VALUES($1,'indexer-b','Other workspace')`, wsB); err != nil {
		t.Fatal(err)
	}
	slow, err := env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Rollback(ctx)
	record := uuid.New()
	if _, err := slow.Exec(ctx, `INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'grounding',1,'paper','grounding','{}')`, record, ws, card); err != nil {
		t.Fatal(err)
	}
	var lower int64
	if err := slow.QueryRow(ctx, `INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,'record.created','system','{}') RETURNING id`, ws, card).Scan(&lower); err != nil {
		t.Fatal(err)
	}
	// Different workspaces serialize separately: the greater sequence ID can commit first.
	if err := x.Store.InWorkspace(ctx, wsB, uuid.Nil, func(tx *store.Tx) error { return tx.Emit(ctx, "workspace.updated", nil, store.System, nil) }); err != nil {
		t.Fatal(err)
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	var higher int64
	if err := env.Owner.QueryRow(ctx, `SELECT max(id) FROM event WHERE workspace_id=$1`, wsB).Scan(&higher); err != nil {
		t.Fatal(err)
	}
	if higher <= lower {
		t.Fatal("bad interleaving fixture")
	}
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "record", ID: record})).Val() != 1 {
		t.Fatal("late lower-ID commit permanently skipped")
	}
	var queued int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM lab_event_delivery`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("delivery rows leaked: %d %v", queued, err)
	}
}

func TestIndexerDeletedWorkspaceNamespaceIsRemoved(t *testing.T) {
	x, env, ws, _ := indexerFixture(t)
	ctx := context.Background()
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 10; n++ {
		if err := x.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := env.Redis.Keys(ctx, "lab:"+ws.String()+":*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("orphan keys %v: %v", keys, err)
	}
}

func TestIndexerRecoversRedisFlushWithoutNewEvents(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if err := env.Redis.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "study", ID: card})).Val() != 1 {
		t.Fatal("flush did not trigger source backfill")
	}
	if err := env.Redis.Do(ctx, "FT.INFO", "idx:lab").Err(); err != nil {
		t.Fatal("search schema not recovered", err)
	}
}

func TestIndexerRecoversTransientRedisStartupFailure(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	good := x.Index.rdb
	bad := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	defer bad.Close()
	x.Index.rdb = bad
	if err := x.Tick(ctx); err == nil {
		t.Fatal("expected Redis readiness failure")
	}
	x.Index.rdb = good
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if env.Redis.Exists(ctx, key(ws, labmemory.Ref{Kind: "study", ID: card})).Val() != 1 {
		t.Fatal("startup recovery failed")
	}
}

type blockingIndexerEmbedder struct{ started chan struct{} }

func (b blockingIndexerEmbedder) Embed(ctx context.Context, _ []string, _ bool) ([][]float32, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestIndexerSingleConnectionAndIndependentCancellation(t *testing.T) {
	x, env, _, _ := indexerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(env.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	x.Store.Pool = pool
	// Complete a real initial backfill with a single connection, not merely an empty tick.
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if err := env.Redis.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	x.Index.e = blockingIndexerEmbedder{started: started}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); x.Run(runCtx, time.Millisecond, nil) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("index loop never reached embedding")
	}
	// While embedding blocks, unrelated engine/database work can use the sole connection.
	var n int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("indexer held the sole connection: %d %v", n, err)
	}
	stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("independent index loop did not cancel")
	}
}

func TestIndexerPagesLargeWorkspace(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	for n := 0; n < 80; n++ {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,'grounding',1,'paper','grounding','{}')`, ws, card); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	keys, err := env.Redis.Keys(ctx, "lab:"+ws.String()+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) > factBudget {
		t.Fatalf("unbounded first page: %d", len(keys))
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	keys, err = env.Redis.Keys(ctx, "lab:"+ws.String()+":*").Result()
	if err != nil || len(keys) != 82 {
		t.Fatalf("backfill incomplete: %d %v", len(keys), err)
	}
}

func TestIndexerSkipsCoresearchRunWithoutCard(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	// Would require embedding if the nil-card lifecycle event incorrectly reconciles this study.
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET title='Changed',version=version+1 WHERE id=$1`, card); err != nil {
		t.Fatal(err)
	}
	x.Index.e = failingEmbedder{}
	if err := x.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		return tx.Emit(ctx, "agent_run.queued", nil, store.System, map[string]any{"chat_id": uuid.New(), "agent_run_id": uuid.New()})
	}); err != nil {
		t.Fatal(err)
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatalf("Coresearch lifecycle triggered indexing: %v", err)
	}
}

func TestIndexerRefreshesDecisionReminder(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	var user uuid.UUID
	if err := env.Owner.QueryRow(ctx, `SELECT owner_id FROM card WHERE id=$1`, card).Scan(&user); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,options) VALUES($1,$2,$3,$4,'agent_decision','Question','[{"id":"a"}]')`, id, ws, card, user); err != nil {
		t.Fatal(err)
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	k := key(ws, labmemory.Ref{Kind: "decision", ID: id})
	before := env.Redis.HGet(ctx, k, "version").Val()
	if _, err := env.Owner.Exec(ctx, `UPDATE inbox_item SET reminded_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	emitIndexEvent(t, x, ws, card, "decision.reminder")
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if after := env.Redis.HGet(ctx, k, "version").Val(); after == "" || after == before {
		t.Fatal("reminder left decision vector version stale")
	}
}

func TestIndexerDeliveryHelpersBoundedAndPrivileged(t *testing.T) {
	x, env, ws, card := indexerFixture(t)
	ctx := context.Background()
	for n := 0; n < 205; n++ {
		emitIndexEvent(t, x, ws, card, "coresearch.message.created")
	}
	var n int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM lab_pending_events('indexer',10000)`).Scan(&n); err != nil || n != 200 {
		t.Fatalf("unbounded delivery: %d %v", n, err)
	}
	for _, table := range []string{"lab_event_delivery", "lab_event_consumer"} {
		if _, err := env.App.Exec(ctx, "SELECT * FROM "+table); err == nil {
			t.Fatalf("app can directly read %s", table)
		}
	}
	for _, name := range []string{"lab_pending_events(text,integer)", "lab_ack_event(text,bigint)", "lab_index_workspaces_after(uuid,integer)", "lab_index_workspace_exists(uuid)"} {
		var public bool
		if err := env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM aclexplode((SELECT proacl FROM pg_proc WHERE oid=$1::regprocedure)) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')`, name).Scan(&public); err != nil || public {
			t.Fatalf("public helper %s: %v %v", name, public, err)
		}
	}
	if err := drainIndexer(ctx, x); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM lab_event_delivery`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("acknowledgements leak rows: %d %v", n, err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace(id,slug,name) SELECT gen_random_uuid(),'bounded-'||n,'Bounded' FROM generate_series(1,25) n`); err != nil {
		t.Fatal(err)
	}
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM lab_index_workspaces_after($1,10000)`, uuid.Nil).Scan(&n); err != nil || n != 20 {
		t.Fatalf("unbounded workspace discovery: %d %v", n, err)
	}
}
