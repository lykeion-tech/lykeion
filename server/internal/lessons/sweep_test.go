package lessons

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/embed"
	"lykeion/server/internal/labindex"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
)

func TestSweepSetsAsideOnChangedFact(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	study := labmemory.Ref{Kind: "study", ID: f.card}
	var version string
	if err := f.x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if err := tx.Workspace(ctx, f.ws); err != nil {
			return err
		}
		var err error
		version, err = labmemory.Version(ctx, tx, study)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cites, _ := json.Marshal([]labmemory.Citation{{Ref: study.String(), Version: version}})
	id := uuid.New()
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','lesson',$3,'test',now())`, id, f.ws, cites)
	if err := f.x.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM lesson WHERE status='active'`) != 1 {
		t.Fatal("fresh lesson set aside")
	}
	f.exec(`UPDATE card SET question='changed' WHERE id=$1`, f.card)
	if err := f.x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if err := tx.Workspace(ctx, f.ws); err != nil {
			return err
		}
		_, err := labmemory.ReadMaintenance(ctx, tx, labmemory.Ref{Kind: "lesson", ID: id})
		if !errors.Is(err, labmemory.ErrNotFound) {
			t.Fatalf("stale lesson readable: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := f.x.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='set_aside' AND set_aside_reason='fact changed'`, id) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1`, id) != 1 {
		t.Fatal("stale lesson not set aside exactly once")
	}
}

type rankEmbed struct {
	t       *testing.T
	queries int
}

func (e *rankEmbed) Embed(_ context.Context, text []string, query bool) ([][]float32, error) {
	v := make([]float32, embed.Dimensions)
	angle := 2.9 // All ordinary fact documents share a distinct direction.
	if query {
		e.queries++
		if text[0] != "Question\nH1" {
			e.t.Fatalf("query %q", text)
		}
		angle = 0
	} else if text[0] == "Question\nH1" {
		angle = 0
	} else if angleString, ok := strings.CutPrefix(text[0], "Question\nangle "); ok {
		var err error
		angle, err = strconv.ParseFloat(angleString, 64)
		if err != nil {
			return nil, err
		}
	}
	v[0] = float32(math.Cos(angle))
	v[1] = float32(math.Sin(angle))
	return [][]float32{v}, nil
}
func TestSimilarStudiesAreFinishedAndNotSelf(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	e := &rankEmbed{t: t}
	idx := labindex.New(f.env.Redis, e, embed.Model)
	if err := idx.Ensure(ctx); errors.Is(err, labindex.ErrUnavailable) {
		t.Skip("requires Redis Search")
	} else if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,'hypotheses',1,'hypothesis','hypotheses','{"text":"H1"}')`, f.ws, f.card)
	var want []uuid.UUID
	// The production indexer sees identical candidate questions and titles.
	// Only hypothesis text distinguishes the near and far candidates.
	for n := 1; n < 115; n++ {
		id := uuid.New()
		state := "working"
		if n >= 108 {
			state = "done"
			want = append(want, id)
		}
		f.exec(`INSERT INTO card(id,workspace_id,number,title,question,owner_id,state,board_column) VALUES($1,$2,$3,'Candidate','Question',$4,$5,'validation')`, id, f.ws, n+1, f.user, state)
		f.exec(`INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,'hypotheses',1,'hypothesis','hypotheses',jsonb_build_object('text',$3::text))`, f.ws, id, "angle "+strconv.FormatFloat(float64(n)*0.011, 'f', 4, 64))
	}
	other, c := uuid.New(), uuid.New()
	f.workspace(other, c)
	f.exec(`UPDATE card SET state='done' WHERE id=$1 OR id=$2`, c, f.card)
	x := &labindex.Indexer{Store: f.x.Store, Index: idx}
	drain := func() {
		t.Helper()
		for n := 0; n < 128; n++ {
			if err := x.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if f.count(`SELECT count(*) FROM lab_event_delivery WHERE consumer='indexer'`) == 0 {
				return
			}
		}
		t.Fatal("indexer did not drain")
	}
	f.emit("record.created", `{}`)
	drain()
	check := func(expected []uuid.UUID) {
		t.Helper()
		if err := f.x.Store.Maintenance(ctx, func(tx *store.Tx) error {
			if err := tx.Workspace(ctx, f.ws); err != nil {
				return err
			}
			got, err := SimilarStudies(ctx, tx, idx, f.card, 5)
			if err == nil && !slices.Equal(got, expected) {
				t.Fatalf("got %v want %v", got, expected)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	check(want[:5])
	// Superseding its only hypothesis leaves the study's record links unchanged.
	// Its ordinary study version cannot be used as the similarity fingerprint.
	f.exec(`UPDATE record SET superseded_at=now() WHERE workspace_id=$1 AND card_id=$2`, f.ws, want[0])
	f.emit("record.superseded", `{}`)
	drain()
	check(want[1:6])
	if e.queries != 2 {
		t.Fatalf("embedded query %d times", e.queries)
	}
	if got, err := SimilarStudies(ctx, nil, nil, f.card, 5); err != nil || got != nil {
		t.Fatalf("nil index: %v %v", got, err)
	}
}

func TestLockedSweepWorkspaceRotatesAndRetries(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	other, card := uuid.New(), uuid.New()
	f.workspace(other, card)
	ws1, ws2 := f.ws, other
	if ws2.String() < ws1.String() {
		ws1, ws2 = ws2, ws1
	}
	blocked, healthy := uuid.New(), uuid.New()
	for _, entry := range []struct{ ws, id uuid.UUID }{{ws1, blocked}, {ws2, healthy}} {
		f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','stale','[]','test',now())`, entry.id, entry.ws)
	}
	locker, err := f.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	if _, err := locker.Exec(ctx, `SELECT id FROM lesson WHERE id=$1 FOR UPDATE`, blocked); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := f.x.Sweep(deadline); err == nil || deadline.Err() != nil {
		t.Fatalf("locked page should fail promptly: %v, context %v", err, deadline.Err())
	}
	f.emit("card.steered", `{"column":"validation"}`)
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job WHERE card_id=$1`, f.card) != 1 {
		t.Fatal("sweep blocked trigger delivery")
	}
	if err := f.x.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1`, healthy) != 1 {
		t.Fatal("healthy workspace starved")
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1`, blocked) != 0 {
		t.Fatal("locked page advanced")
	}
	if err := locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.x.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1`, blocked) != 1 {
		t.Fatal("failed page was not retried")
	}
}

func TestSweepWaitingOnDatabaseLeavesTriggersRunnable(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	id := uuid.New()
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','stale','[]','test',now())`, id, f.ws)
	f.exec(`CREATE FUNCTION pause_sweep_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(98765,43210); RETURN NEW; END $$;
 CREATE TRIGGER pause_sweep BEFORE UPDATE ON lesson FOR EACH ROW EXECUTE FUNCTION pause_sweep_test()`)
	locker, err := f.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	if _, err := locker.Exec(ctx, `SELECT pg_advisory_xact_lock(98765,43210)`); err != nil {
		t.Fatal(err)
	}
	sweepDone := make(chan error, 1)
	go func() { sweepDone <- f.x.Sweep(ctx) }()
	until := time.Now().Add(2 * time.Second)
	for f.count(`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND classid=98765 AND objid=43210 AND NOT granted`) == 0 {
		if time.Now().After(until) {
			t.Fatal("sweep never reached injected wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The sweep is demonstrably in flight when the producer commits and the trigger runs.
	f.emit("card.steered", `{"column":"validation"}`)
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := f.x.Tick(deadline); err != nil || deadline.Err() != nil {
		t.Fatalf("blocked sweep prevented trigger: %v %v", err, deadline.Err())
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE card_id=$1`, f.card) != 1 {
		t.Fatal("trigger did not commit during sweep")
	}
	if err := locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-sweepDone; err != nil {
		t.Fatal(err)
	}
}

func TestSimilaritySchemaUpgradePreservesOrdinarySearch(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	if err := f.env.Redis.Do(ctx, "FT.CREATE", "idx:lab", "ON", "HASH", "PREFIX", 1, "lab:", "SCHEMA", "workspace", "TAG", "kind", "TAG", "model", "TAG", "embedding", "VECTOR", "FLAT", 6, "TYPE", "FLOAT32", "DIM", embed.Dimensions, "DISTANCE_METRIC", "COSINE").Err(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unknown command") {
			t.Skip("requires Redis Search")
		}
		t.Fatal(err)
	}
	idx := labindex.New(f.env.Redis, embed.Fake{}, embed.Model)
	r := labmemory.Ref{Kind: "study", ID: f.card}
	if err := idx.Put(ctx, f.ws, r, "old", "ordinary search"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := idx.Search(ctx, f.ws, "ordinary search", 1)
	if err != nil || len(got) != 1 || got[0] != r {
		t.Fatalf("ordinary search lost its study on upgrade: %v %v", got, err)
	}
	var candidates []labmemory.Ref
	err = idx.VisitStudyCandidates(ctx, f.ws, "ordinary search", func(refs []labmemory.Ref) (bool, error) { candidates = append(candidates, refs...); return false, nil })
	if err != nil || len(candidates) != 0 {
		t.Fatalf("ordinary vector used as a lesson vector: %v %v", candidates, err)
	}
}
