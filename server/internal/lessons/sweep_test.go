package lessons

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"

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
	angle := 0.0
	if query {
		e.queries++
		if text[0] != "Question\nH1" {
			e.t.Fatalf("query %q", text)
		}
	} else {
		var err error
		angle, err = strconv.ParseFloat(text[0], 64)
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
	// More than 100 nearer active or missing refs; first 5 matches require overfetch.
	for n := 0; n < 114; n++ {
		id := uuid.New()
		if n == 0 {
			id = f.card
		}
		if n > 0 && (n%2 == 0 || n >= 108) {
			state := "working"
			if n >= 108 {
				state = "done"
				if len(want) < 5 {
					want = append(want, id)
				}
			}
			f.exec(`INSERT INTO card(id,workspace_id,number,title,question,owner_id,state,board_column) VALUES($1,$2,$3,'Test','Q',$4,$5,'validation')`, id, f.ws, n+1, f.user, state)
		}
		if err := idx.Put(ctx, f.ws, labmemory.Ref{Kind: "study", ID: id}, "v", strconv.FormatFloat(float64(n)*0.011, 'f', 4, 64)); err != nil {
			t.Fatal(err)
		}
	}
	other, c := uuid.New(), uuid.New()
	f.workspace(other, c)
	f.exec(`UPDATE card SET state='done' WHERE id=$1`, c)
	if err := idx.Put(ctx, other, labmemory.Ref{Kind: "study", ID: c}, "v", "0"); err != nil {
		t.Fatal(err)
	}
	if err := f.x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if err := tx.Workspace(ctx, f.ws); err != nil {
			return err
		}
		got, err := SimilarStudies(ctx, tx, idx, f.card, 5)
		if err != nil {
			return err
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if e.queries != 1 {
		t.Fatalf("embedded query %d times", e.queries)
	}
	if got, err := SimilarStudies(ctx, nil, nil, f.card, 5); err != nil || got != nil {
		t.Fatalf("nil index: %v %v", got, err)
	}
}
