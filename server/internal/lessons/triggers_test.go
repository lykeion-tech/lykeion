package lessons

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

type fixture struct {
	t              *testing.T
	env            *testutil.Env
	x              *Triggers
	ws, card, user uuid.UUID
	now            time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, env: testutil.Setup(t), ws: uuid.New(), card: uuid.New(), user: uuid.New(), now: time.Now().UTC().Truncate(time.Second)}
	f.x = &Triggers{Store: &store.Store{LessonsEnabled: true, Pool: f.env.App}, Now: func() time.Time { return f.now }}
	f.exec(`INSERT INTO "user"(id,email,name) VALUES($1,'lesson@example.org','Researcher')`, f.user)
	f.workspace(f.ws, f.card)
	return f
}
func (f *fixture) workspace(ws, card uuid.UUID) {
	f.exec(`INSERT INTO workspace(id,slug,name) VALUES($1,$2,'Lessons')`, ws, ws.String())
	f.exec(`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'Study','Question',$3,'grounding')`, card, ws, f.user)
}
func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, e := f.env.Owner.Exec(f.t.Context(), q, args...); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) emit(typ string, payload string) {
	f.exec(`INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,$3,'system',$4)`, f.ws, f.card, typ, payload)
}
func (f *fixture) tick() {
	f.t.Helper()
	if err := f.x.Tick(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.env.Owner.QueryRow(f.t.Context(), q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *fixture) wantJob(want ...string) {
	f.t.Helper()
	var triggers []string
	var due time.Time
	if err := f.env.Owner.QueryRow(f.t.Context(), `SELECT triggers,due_at FROM lesson_job WHERE card_id=$1`, f.card).Scan(&triggers, &due); err != nil {
		f.t.Fatal(err)
	}
	if !slices.Equal(triggers, want) {
		f.t.Fatalf("got %v want %v", triggers, want)
	}
}
func TestColumnMoveQueuesMovedJob(t *testing.T) {
	f := setup(t)
	f.emit("card.steered", `{"column":"hypotheses"}`)
	f.tick()
	f.wantJob("moved:grounding")
}
func TestFinishQueuesFinishedJob(t *testing.T) {
	f := setup(t)
	f.emit("card.steered", `{"state":"done","column":"grounding"}`)
	f.tick()
	f.wantJob("finished")
}
func TestTriggersWithinWindowMerge(t *testing.T) {
	f := setup(t)
	start := f.now
	for _, col := range []string{"hypotheses", "experimentation", "validation"} {
		f.emit("card.steered", `{"column":"`+col+`"}`)
		f.tick()
		f.now = f.now.Add(5 * time.Minute)
	}
	f.wantJob("moved:grounding", "moved:hypotheses", "moved:experimentation")
	if n := f.count(`SELECT count(*) FROM lesson_job WHERE due_at=$1`, start.Add(15*time.Minute)); n != 1 {
		t.Fatalf("jobs due at first window: %d", n)
	}
}
func TestDecisionAndInformation(t *testing.T) {
	f := setup(t)
	for _, status := range []string{"decided", "expired"} {
		id := uuid.New()
		f.exec(`INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,question_set,status) VALUES($1,$2,$3,$4,'agent_decision','Q','{}',$5)`, id, f.ws, f.card, f.user, status)
		f.emit("inbox.resolved", `{"inbox_item_id":"`+id.String()+`"}`)
		f.tick()
		if f.count(`SELECT count(*) FROM lesson_job WHERE $1=ANY(triggers)`, "decision:"+id.String()) != 1 {
			t.Fatal("missing", status)
		}
	}
	id := uuid.New()
	f.exec(`INSERT INTO inbox_item(id,workspace_id,card_id,user_id,kind,text,status) VALUES($1,$2,$3,$4,'done','Q','decided')`, id, f.ws, f.card, f.user)
	f.emit("inbox.resolved", `{"inbox_item_id":"`+id.String()+`"}`)
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job WHERE $1=ANY(triggers)`, "decision:"+id.String()) != 0 {
		t.Fatal("information item generated job")
	}
}

func TestLowerIDLateCommitAndIndependentAcknowledgement(t *testing.T) {
	f := setup(t)
	other, otherCard := uuid.New(), uuid.New()
	f.workspace(other, otherCard)
	ctx := t.Context()
	slow, err := f.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Rollback(ctx)
	var lower int64
	if err := slow.QueryRow(ctx, `INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,'card.steered','system','{"column":"hypotheses"}') RETURNING id`, f.ws, f.card).Scan(&lower); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,'card.steered','system','{"state":"done"}')`, other, otherCard)
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job WHERE workspace_id=$1 AND triggers=ARRAY['finished']`, other) != 1 {
		t.Fatal("higher committed job missing")
	}
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	f.tick()
	f.wantJob("moved:grounding")
	if f.count(`SELECT count(*) FROM lab_event_delivery WHERE consumer='indexer' AND event_id=$1`, lower) != 1 || f.count(`SELECT count(*) FROM lab_event_delivery WHERE consumer='lessons'`) != 0 {
		t.Fatal("acknowledged wrong consumer or missed late event")
	}
}

func TestFailedQueueRollsBackAndRetries(t *testing.T) {
	f := setup(t)
	f.emit("card.steered", `{"column":"hypotheses"}`)
	f.exec(`ALTER TABLE lesson_job ADD CHECK (false)`)
	if err := f.x.Tick(t.Context()); err == nil {
		t.Fatal("queue failure not returned")
	}
	if f.count(`SELECT count(*) FROM lab_event_delivery WHERE consumer='lessons'`) != 1 || f.count(`SELECT count(*) FROM lesson_card WHERE board_column='grounding'`) != 1 {
		t.Fatal("failure advanced baseline or acknowledged")
	}
	f.exec(`ALTER TABLE lesson_job DROP CONSTRAINT lesson_job_check`)
	f.tick()
	f.tick()
	f.wantJob("moved:grounding")
	if f.count(`SELECT count(*) FROM lesson_job`) != 1 {
		t.Fatal("duplicate after retry")
	}
}
func TestReopenedAndPrivateEventsDoNotTrigger(t *testing.T) {
	f := setup(t)
	f.emit("card.steered", `{"state":"done","column":"grounding"}`)
	f.tick()
	f.exec(`UPDATE lesson_job SET status='submitted',batch_id='batch'`)
	f.emit("card.steered", `{"state":"working","column":"validation"}`)
	f.emit("coresearch.message.created", `{"state":"done"}`)
	f.emit("validation.updated", `{"state":"done"}`)
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job`) != 1 {
		t.Fatal("reopening, validation or private event generated job")
	}
	f.emit("card.steered", `{"column":"experimentation","state":"working"}`)
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job WHERE status='queued' AND triggers=ARRAY['moved:validation']`) != 1 {
		t.Fatal("missing fresh job after submission")
	}
}

func TestMigrationSeedsExistingCardsAtCutover(t *testing.T) {
	f := setup(t)
	// Later migrations build on lab_chat, so they come off before 0052 and go
	// back on after it.
	later := []string{"0053_lab_chat_deletion", "0054_manual_decisions", "0055_manual_decision_drafts", "0056_manual_draft_archives", "0057_manual_draft_application", "0058_project_delete", "0059_decision_builder_machine", "0060_decision_draft_receipt"}
	apply := func(name string) {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(string(sql))
	}
	for i := len(later) - 1; i >= 0; i-- {
		apply(later[i] + ".down.sql")
	}
	for _, direction := range []string{"down", "up"} {
		if direction == "up" {
			f.exec(`UPDATE card SET board_column='experimentation' WHERE id=$1`, f.card)
			// Historical moves happened before lessons began and must never be replayed backwards.
			f.emit("card.steered", `{"column":"hypotheses"}`)
		}
		apply("0052_aristotle." + direction + ".sql")
	}
	for _, name := range later {
		apply(name + ".up.sql")
	}
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job`) != 0 {
		t.Fatal("historical move replayed against new baseline")
	}
	f.emit("card.steered", `{"column":"validation"}`)
	f.tick()
	f.wantJob("moved:experimentation")
}

func TestNextWindowDoesNotExtendOverdueJob(t *testing.T) {
	f := setup(t)
	f.emit("card.steered", `{"column":"hypotheses"}`)
	f.tick()
	f.now = f.now.Add(16 * time.Minute)
	f.emit("card.steered", `{"column":"experimentation"}`)
	f.tick()
	if f.count(`SELECT count(*) FROM lesson_job WHERE triggers=ARRAY['moved:grounding']`) != 1 || f.count(`SELECT count(*) FROM lesson_job WHERE triggers=ARRAY['moved:hypotheses'] AND due_at=$1`, f.now.Add(15*time.Minute)) != 1 {
		t.Fatal("delayed batch writer swallowed a later window")
	}
}
