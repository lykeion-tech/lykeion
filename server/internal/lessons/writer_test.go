package lessons

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"lykeion/server/internal/store"
	"strings"
	"sync"
	"testing"
)

type fakeBatch struct {
	mu       sync.Mutex
	requests [][]Request
	output   Output
	err      error
}

func (b *fakeBatch) Submit(_ context.Context, r []Request) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests = append(b.requests, r)
	return fmt.Sprint(len(b.requests)), b.err
}
func (b *fakeBatch) Collect(_ context.Context, batch string) ([]Result, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, reqs := range b.requests {
		if fmt.Sprint(i+1) == batch {
			var results []Result
			for _, r := range reqs {
				results = append(results, Result{JobID: r.JobID, Output: b.output})
			}
			return results, true, b.err
		}
	}
	return nil, false, errors.New("unknown batch")
}

type writingFixture struct {
	*fixture
	w     *Writer
	b     *fakeBatch
	rec   uuid.UUID
	draft Draft
}

func writing(t *testing.T) *writingFixture {
	f := setup(t)
	b := &fakeBatch{}
	x := &writingFixture{fixture: f, b: b, rec: uuid.New(), w: &Writer{Store: f.x.Store, Batcher: b, Jev: fakeJudge(t, "supported")}}
	f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'hypotheses',1,'hypothesis','hypotheses','{"text":"Shared hypothesis"}')`, x.rec, f.ws, f.card)
	x.draft = Draft{Kind: "lab", Text: "A shared hypothesis was recorded.", Cites: []string{"record:" + x.rec.String()}}
	b.output = Output{Lessons: []Draft{x.draft}}
	return x
}
func (f *writingFixture) queue() uuid.UUID {
	id := uuid.New()
	f.exec(`INSERT INTO lesson_job(id,workspace_id,card_id,triggers,due_at) VALUES($1,$2,$3,ARRAY['moved:hypotheses'],now())`, id, f.ws, f.card)
	return id
}
func (f *writingFixture) run() {
	f.t.Helper()
	if err := f.w.Tick(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}
func (f *writingFixture) create() uuid.UUID {
	f.t.Helper()
	f.queue()
	f.run()
	f.run()
	var id uuid.UUID
	if err := f.env.Owner.QueryRow(f.t.Context(), `SELECT id FROM lesson`).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func TestAcceptedLessonStoresVersionsAndModel(t *testing.T) {
	f := writing(t)
	id := f.create()
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND model=$2 AND checked_at IS NOT NULL AND studies=ARRAY[$3::uuid] AND cites->0->>'ref'=$4 AND cites->0->>'version'='live'`, id, Model, f.card, f.draft.Cites[0]) != 1 {
		t.Fatal("missing checked evidence")
	}
	if f.count(`SELECT count(*) FROM lab_event_delivery d JOIN event e ON e.id=d.event_id WHERE d.consumer='indexer' AND e.type='lesson.updated'`) != 1 {
		t.Fatal("missing durable indexing work")
	}
}
func TestCounterSetsLessonAsideWhenItNoLongerHolds(t *testing.T) {
	f := writing(t)
	id := f.create()
	f.b.output = Output{Counters: []Counter{{Lesson: "lesson:" + id.String(), Fact: f.draft.Cites[0]}}}
	f.w.Jev = fakeJudge(t, "no_longer_holds")
	f.queue()
	f.run()
	f.run()
	if f.count(`SELECT count(*) FROM lesson_counter WHERE lesson_id=$1`, id) != 1 || f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='set_aside'`, id) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1`, id) != 1 {
		t.Fatal("missing counter, set-aside or rewrite")
	}
}
func TestRewriteReplacesOrDeletes(t *testing.T) {
	f := writing(t)
	id := f.create()
	flag := func() {
		f.exec(`UPDATE lesson SET status='set_aside',set_aside_reason='flagged' WHERE id=$1`, id)
		f.exec(`INSERT INTO lesson_flag(workspace_id,lesson_id,user_id,reason) VALUES($1,$2,$3,'tighten wording')`, f.ws, id, f.user)
		f.exec(`INSERT INTO lesson_job(workspace_id,lesson_id,triggers,due_at) VALUES($1,$2,ARRAY['flag'],now())`, f.ws, id)
	}
	flag()
	d := f.draft
	d.Text = "A narrower claim."
	f.b.output = Output{Replacement: &d}
	f.run()
	if !strings.Contains(f.b.requests[len(f.b.requests)-1][0].Prompt, "tighten wording") {
		t.Fatal("flag absent")
	}
	f.run()
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='active' AND text=$2 AND model=$3 AND cites->0->>'version'='live'`, id, d.Text, Model) != 1 {
		t.Fatal("replacement not guarded/versioned")
	}
	flag()
	d.Cites = nil
	f.run()
	f.run()
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='set_aside'`, id) != 1 {
		t.Fatal("accepted unsupported replacement")
	}
	flag()
	f.b.output = Output{}
	f.run()
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 0 {
		t.Fatal("null replacement did not delete")
	}
}
func TestPromptNeverContainsCoresearchText(t *testing.T) {
	f := writing(t)
	project, chat := uuid.New(), uuid.New()
	f.exec(`INSERT INTO coresearch_project(id,workspace_id,name,created_by) VALUES($1,$2,'private project',$3)`, project, f.ws, f.user)
	f.exec(`INSERT INTO coresearch_chat(id,workspace_id,project_id,owner_id,mode,cli) VALUES($1,$2,$3,$4,'experiment','claude')`, chat, f.ws, project, f.user)
	f.exec(`INSERT INTO coresearch_message(workspace_id,chat_id,kind,body) VALUES($1,$2,'comment','private-coresearch-marker')`, f.ws, chat)
	f.queue()
	f.run()
	if len(f.b.requests) != 1 {
		t.Fatal("no request")
	}
	p := f.b.requests[0][0].Prompt
	if strings.Contains(p, "private-coresearch-marker") || !strings.Contains(p, "Shared hypothesis") {
		t.Fatal("wrong prompt projection")
	}
}
func TestFailedProviderRetriesAndConcurrentTicksApplyOnce(t *testing.T) {
	f := writing(t)
	job := f.queue()
	f.b.err = errors.New("provider unavailable")
	if f.w.Tick(t.Context()) == nil {
		t.Fatal("expected failure")
	}
	f.exec(`UPDATE lesson_job SET due_at=now() WHERE id=$1`, job)
	f.b.err = nil
	f.run()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.w.Tick(t.Context()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.count(`SELECT count(*) FROM lesson`) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE status='done'`) != 1 {
		t.Fatal("duplicate or lost job")
	}
}
func TestChangedSubmittedFactCannotWriteLesson(t *testing.T) {
	f := writing(t)
	f.queue()
	f.run()
	f.exec(`UPDATE record SET superseded_at=now() WHERE id=$1`, f.rec)
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 0 {
		t.Fatal("accepted withdrawn evidence")
	}
}
func TestGuardRejectsEmptyAndLessonCites(t *testing.T) {
	f := writing(t)
	if err := f.w.scoped(t.Context(), f.ws, func(tx *store.Tx) error {
		for _, refs := range [][]string{nil, {"lesson:" + uuid.NewString()}} {
			d := f.draft
			d.Cites = refs
			if ok, _ := f.w.Guard(t.Context(), tx, d); ok {
				t.Fatal("non-factual cite accepted")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriterBatchLimitAndJevRetry(t *testing.T) {
	f := writing(t)
	for range 51 {
		f.queue()
	}
	f.run()
	if len(f.b.requests) != 1 || len(f.b.requests[0]) != 50 || f.count(`SELECT count(*) FROM lesson_job WHERE status='queued'`) != 1 {
		t.Fatal("unexpected batch size")
	}
	// Exercise an interrupted guard for just one collected result.
	first := f.b.requests[0][0].JobID
	f.exec(`DELETE FROM lesson_job WHERE id <> $1`, first)
	f.w.Jev = fakeJudge(t, "provider-malformed")
	if err := f.w.Tick(t.Context()); err == nil {
		t.Fatal("missing Jev failure")
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE id=$1 AND status='submitted'`, first) != 1 || f.count(`SELECT count(*) FROM lesson`) != 0 {
		t.Fatal("failed guard mutated lesson")
	}
	f.w.Jev = fakeJudge(t, "supported")
	f.exec(`UPDATE lesson_job SET due_at=now() WHERE id=$1`, first)
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 1 {
		t.Fatal("failed Jev guard did not retry")
	}
}
