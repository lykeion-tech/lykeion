package lessons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBatch struct {
	mu       sync.Mutex
	requests [][]Request
	output   Output
	err      error
}

func (b *fakeBatch) Submit(ctx context.Context, r []Request) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
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

func (f *writingFixture) rewrite(refs []string) uuid.UUID {
	f.t.Helper()
	cites := []labmemory.Citation{}
	for _, ref := range refs {
		cites = append(cites, labmemory.Citation{Ref: ref, Version: "live"})
	}
	raw, err := json.Marshal(cites)
	if err != nil {
		f.t.Fatal(err)
	}
	id := uuid.New()
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,status,model,checked_at) VALUES($1,$2,'lab','Previous lesson',$3,'set_aside','test',now())`, id, f.ws, raw)
	f.exec(`INSERT INTO lesson_job(workspace_id,lesson_id,triggers,due_at) VALUES($1,$2,ARRAY['flag'],now())`, f.ws, id)
	return id
}
func TestLargeRewriteCannotReplaceOrDelete(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint("replace-", replace), func(t *testing.T) {
			f := writing(t)
			refs := []string{}
			for range 13 {
				id := uuid.New()
				f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'hypotheses',1,'hypothesis','hypotheses','{"text":"positive"}')`, id, f.ws, f.card)
				refs = append(refs, "record:"+id.String())
			}
			target := f.rewrite(refs)
			counter := uuid.New()
			f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'hypotheses',1,'hypothesis','hypotheses','{"text":"sole-contradiction"}')`, counter, f.ws, f.card)
			f.exec(`INSERT INTO lesson_counter(workspace_id,lesson_id,ref) VALUES($1,$2,$3)`, f.ws, target, "record:"+counter.String())
			f.b.output = Output{}
			if replace {
				d := f.draft
				d.Cites = refs[:1]
				f.b.output.Replacement = &d
			}
			f.run()
			if len(f.b.requests) != 1 || !strings.Contains(f.b.requests[0][0].Prompt, "sole-contradiction") {
				t.Fatal("counter missing")
			}
			// Simulate an already-submitted batch whose dependencies now exceed the
			// budget. Both output shapes must fail closed on collection as well.
			f.exec(`UPDATE record SET body=jsonb_build_object('text',repeat('positive',1400)) WHERE workspace_id=$1 AND id<>$2`, f.ws, counter)
			f.run()
			if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='set_aside'`, target) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE status='failed'`) != 1 {
				t.Fatal("oversized rewrite applied")
			}
			f.exec(`UPDATE lesson_job SET status='queued',due_at=now() WHERE lesson_id=$1`, target)
			f.run()
			if len(f.b.requests) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE status='failed'`) != 1 {
				t.Fatal("submitted truncated evidence")
			}
		})
	}
}
func TestRestoredCounterInvalidatesPendingRewrite(t *testing.T) {
	f := writing(t)
	target := f.rewrite(f.draft.Cites)
	counter := uuid.New()
	ref := "record:" + counter.String()
	f.exec(`INSERT INTO lesson_counter(workspace_id,lesson_id,ref) VALUES($1,$2,$3)`, f.ws, target, ref)
	f.b.output = Output{}
	f.run()
	if len(f.b.requests) != 0 {
		t.Fatal("sent incomplete counter evidence")
	}
	f.exec(`UPDATE lesson_job SET due_at=now() WHERE lesson_id=$1`, target)
	f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'hypotheses',1,'hypothesis','hypotheses','{"text":"restored counter"}')`, counter, f.ws, f.card)
	f.run()
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='set_aside'`, target) != 1 || len(f.b.requests) != 1 || !strings.Contains(f.b.requests[0][0].Prompt, "restored counter") {
		t.Fatal("obsolete deletion accepted")
	}
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 0 {
		t.Fatal("current deletion not accepted")
	}
}
func TestPermanentJevFailuresStopJob(t *testing.T) {
	for _, body := range []string{"max_tokens_exceeded", "refused"} {
		t.Run(body, func(t *testing.T) {
			f := writing(t)
			f.queue()
			f.run()
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, body, 400) }))
			defer s.Close()
			f.w.Jev.BaseURL = s.URL
			if err := f.w.Tick(t.Context()); err == nil {
				t.Fatal("missing permanent failure")
			}
			if f.count(`SELECT count(*) FROM lesson_job WHERE status='failed' AND failures=1`) != 1 || f.count(`SELECT count(*) FROM lesson`) != 0 {
				t.Fatal("permanent failure retried")
			}
		})
	}
}
func TestTransientFailuresExhaustPersistedBudget(t *testing.T) {
	f := writing(t)
	job := f.queue()
	f.b.err = errors.New("temporary outage")
	for range 3 {
		f.exec(`UPDATE lesson_job SET due_at=now() WHERE id=$1`, job)
		if err := f.w.Tick(t.Context()); err == nil {
			t.Fatal("missing transient failure")
		}
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE id=$1 AND failures=3 AND status='failed'`, job) != 1 {
		t.Fatal("exhausted job not terminal")
	}
	f.run()
	if len(f.b.requests) != 3 {
		t.Fatal("submitted failed job")
	}
}

func TestLegacySubmittedSnapshotRebuildsBeforeApplying(t *testing.T) {
	f := writing(t)
	f.queue()
	f.run()
	f.exec(`UPDATE lesson_job_input SET versions=versions->'included'`)
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 0 || len(f.b.requests) != 2 {
		t.Fatal("applied legacy snapshot without a scoped allowlist")
	}
	f.run()
	if f.count(`SELECT count(*) FROM lesson`) != 1 {
		t.Fatal("fresh snapshot did not apply")
	}
}
func TestGuardTimeoutConsumesRetryBudget(t *testing.T) {
	f := writing(t)
	f.queue()
	f.run()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(180 * time.Millisecond) }))
	defer s.Close()
	f.w.Jev.BaseURL = s.URL
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := f.w.collect(ctx); err == nil {
		t.Fatal("expected guard deadline")
	}
	if f.count(`SELECT count(*) FROM lesson_job WHERE failures=1 AND status='submitted'`) != 1 {
		t.Fatal("failed to persist timeout receipt")
	}
}

func TestGuardEvaluatesCompleteRewriteIncludingCounter(t *testing.T) {
	f := writing(t)
	target := f.rewrite(f.draft.Cites)
	ref := "comment:" + uuid.NewString()
	c, _ := labmemory.ParseRef(ref)
	f.exec(`INSERT INTO comment(id,workspace_id,card_id,author_id,body) VALUES($1,$2,$3,$4,repeat('context ',1500)||'negative verdict at end of evidence')`, c.ID, f.ws, f.card, f.user)
	f.exec(`INSERT INTO lesson_counter(workspace_id,lesson_id,ref) VALUES($1,$2,$3)`, f.ws, target, ref)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ State string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var st struct {
			Rewrite promptData `json:"rewrite_evidence"`
		}
		if err := json.Unmarshal([]byte(body.State), &st); err != nil {
			t.Error(err)
		}
		choice := "supported"
		if len(st.Rewrite.Counters) == 1 && st.Rewrite.Counters[0] == ref && strings.Contains(body.State, "negative verdict at end of evidence") {
			choice = "not_supported"
		}
		fmt.Fprintf(w, `{"model":"jev-1.13.0","answers":{"q":{"choice":"%s"}}}`, choice)
	}))
	defer s.Close()
	f.w.Jev.BaseURL = s.URL
	f.b.output = Output{Replacement: &f.draft}
	f.run()
	f.run()
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='set_aside'`, target) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1 AND status='done' AND failures=0`, target) != 1 || len(f.b.requests) != 1 {
		t.Fatal("unchanged contradictory rewrite did not finish as rejected")
	}
}
func TestSlowPreparationLeavesHealthyJobsProviderBudgetIntact(t *testing.T) {
	f := writing(t)
	before := f.queue()
	target := f.rewrite(f.draft.Cites)
	after := f.queue()
	f.exec(`UPDATE lesson_job SET due_at=now()-interval '3 hours' WHERE id=$1`, before)
	f.exec(`UPDATE lesson_job SET due_at=now()-interval '2 hours' WHERE lesson_id=$1`, target)
	f.exec(`UPDATE lesson_job SET due_at=now()-interval '1 hour' WHERE id=$1`, after)
	blocker, err := f.env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(t.Context())
	if _, err = blocker.Exec(t.Context(), `LOCK TABLE lesson_flag IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if err = f.w.submitPrepared(t.Context(), t.Context(), 150*time.Millisecond); err == nil {
		t.Fatal("expected slow rewrite timeout")
	}
	if len(f.b.requests) != 1 || len(f.b.requests[0]) != 2 || f.count(`SELECT count(*) FROM lesson_job WHERE failures=0 AND status='submitted' AND id IN($1,$2)`, before, after) != 2 || f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1 AND status='queued' AND failures=0`, target) != 1 {
		t.Fatal("slow preparation consumed healthy jobs' retries")
	}
}

func TestPreparationDeadlineAllowsPreparedBatchToSubmit(t *testing.T) {
	f := writing(t)
	healthy := f.queue()
	target := f.rewrite(f.draft.Cites)
	f.exec(`UPDATE lesson_job SET due_at=now()-interval '2 hours' WHERE id=$1`, healthy)
	f.exec(`UPDATE lesson_job SET due_at=now()-interval '1 hour' WHERE lesson_id=$1`, target)
	blocker, err := f.env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(t.Context())
	if _, err = blocker.Exec(t.Context(), `LOCK TABLE lesson_flag IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	prep, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err = f.w.submitPrepared(t.Context(), prep, time.Second); err == nil {
		t.Fatal("expected preparation deadline")
	}
	if !errors.Is(prep.Err(), context.DeadlineExceeded) || len(f.b.requests) != 1 || len(f.b.requests[0]) != 1 || f.b.requests[0][0].JobID != healthy || f.count(`SELECT count(*) FROM lesson_job WHERE failures>0`) != 0 {
		t.Fatal("preparation deadline spent provider attempts")
	}
}

func TestRewriteRetriesStudyChangeDuringSupportedJevCall(t *testing.T) {
	f := writing(t)
	study := labmemory.Ref{Kind: "study", ID: f.card}
	version := func() string {
		t.Helper()
		var v string
		err := f.w.scoped(t.Context(), f.ws, func(tx *store.Tx) error {
			var err error
			v, err = labmemory.Version(t.Context(), tx, study)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := version()
	target := f.rewrite([]string{study.String()})
	cites, err := json.Marshal([]labmemory.Citation{{Ref: study.String(), Version: initial}})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE lesson SET cites=$1 WHERE id=$2`, cites, target)
	d := Draft{Kind: "lab", Text: "Rewrite from the original question", Cites: []string{study.String()}}
	f.b.output = Output{Replacement: &d}
	f.run()

	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := f.env.Owner.Exec(r.Context(), `UPDATE card SET question='Revised question during guard' WHERE id=$1`, f.card); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{"q":{"choice":"supported"}}}`)
	}))
	defer judge.Close()
	f.w.Jev.BaseURL = judge.URL
	f.run()
	if len(f.b.requests) != 2 || !strings.Contains(f.b.requests[1][0].Prompt, "Revised question during guard") {
		t.Fatal("stale supported verdict did not produce a new batch for the current question")
	}
	if f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND text='Previous lesson' AND status='set_aside'`, target) != 1 ||
		f.count(`SELECT count(*) FROM lesson_job WHERE lesson_id=$1 AND status='submitted' AND failures=0`, target) != 1 {
		t.Fatal("obsolete rewrite activated or lost its pending job")
	}
	d.Text = "Rewrite from the revised question"
	f.w.Jev = fakeJudge(t, "supported")
	f.run()
	current := version()
	if current == initial || f.count(`SELECT count(*) FROM lesson WHERE id=$1 AND status='active' AND text=$2 AND cites->0->>'version'=$3`, target, d.Text, current) != 1 {
		t.Fatal("fresh rewrite did not pin the revised study")
	}
}
