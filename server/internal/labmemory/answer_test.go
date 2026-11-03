package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"sync"
	"testing"
	"time"
)

type turn struct {
	*fixture
	runs *service.AgentRuns
	run  db.AgentRun
	ref  service.RuntimeRef
	h    handin.HandIns
}

func runningTurn(t *testing.T) *turn {
	f, runs, chat := labQueue(t)
	run := enqueueQuestion(t, f, runs, chat, "private question", "")
	rows, e := runs.Register(t.Context(), f.user, f.ws, "personal", "machine", []string{"claude"}, nil, agent.Platform{OS: "linux"})
	if e != nil {
		t.Fatal(e)
	}
	ref := service.RuntimeRef{WorkspaceID: f.ws, RuntimeID: rows[0].ID}
	claims, e := runs.Claim(t.Context(), f.user, []service.RuntimeRef{ref}, 1)
	if e != nil || len(claims) != 1 {
		t.Fatal(e, claims)
	}
	ref.Attempt = claims[0].AgentRun.Attempt
	if e = runs.Start(t.Context(), f.user, ref, run.ID); e != nil {
		t.Fatal(e)
	}
	return &turn{f, runs, run, ref, handin.HandIns{Runs: runs, Aristotle: Answers{Store: runs.Store}}}
}
func (x *turn) log(ref, via string) {
	x.exec("INSERT INTO lab_read(workspace_id,agent_run_id,ref,via) VALUES($1,$2,$3,$4)", x.ws, x.run.ID, ref, via)
}
func (x *turn) submit(ctx context.Context, kind string, cites ...string) ([]string, error) {
	if cites == nil {
		cites = []string{}
	}
	raw, _ := json.Marshal(map[string]any{"statements": []any{handin.Statement{Kind: kind, Text: "Checked.", Cites: cites}}})
	return x.h.Submit(ctx, x.user, x.ref, x.run.ID, raw)
}
func TestAnswerAcceptsCitedFacts(t *testing.T) {
	x := runningTurn(t)
	from := Ref{"study", x.study}.String()
	to := Ref{"record", x.record}.String()
	x.log(from, "links")
	x.log(to, from)
	x.log(to, from)
	p, e := x.submit(t.Context(), "fact", to)
	if e != nil || len(p) != 0 {
		t.Fatal(e, p)
	}
	var statements, evidence string
	if err := x.env.Owner.QueryRow(t.Context(), "SELECT statements::text,evidence::text FROM lab_message WHERE agent_run_id=$1 AND kind='answer'", x.run.ID).Scan(&statements, &evidence); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statements, "hypothesis") || !strings.Contains(evidence, from) || !strings.Contains(evidence, "hypothesis") || !strings.Contains(evidence, `"via": "`+from+`"`) {
		t.Fatal(statements, evidence)
	}
}
func TestAnswerRejectsInvalidStatements(t *testing.T) {
	x := runningTurn(t)
	r := Ref{"record", x.record}.String()
	l := Ref{"lesson", uuid.New()}.String()
	for _, tc := range []struct {
		name, kind string
		refs       []string
	}{
		{"UnreadCite", "fact", []string{r}}, {"FactCitingLesson", "fact", []string{l}}, {"GapWithCites", "gap", []string{r}}, {"FactWithoutCites", "fact", nil}, {"LessonWithoutCites", "lesson", nil}, {"Malformed", "fact", []string{"coresearch:no"}}, {"LessonWithTwoCites", "lesson", []string{l, l}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := x.submit(t.Context(), tc.kind, tc.refs...)
			if e != nil || len(p) == 0 {
				t.Fatal(e, p)
			}
		})
	}
	var n int
	if err := x.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_message WHERE agent_run_id=$1 AND kind='answer'", x.run.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal(err, n)
	}
}
func TestAnswerRejectsLessonWhoseFactChanged(t *testing.T) {
	x := runningTurn(t)
	l := uuid.New()
	ref := Ref{"lesson", l}.String()
	x.exec("INSERT INTO lesson(id,workspace_id,kind,text,cites,checked_at,model) VALUES($1,$2,'lab','Lesson',$3,now(),'test')", l, x.ws, `[{"ref":"record:`+x.record.String()+`","version":"live"}]`)
	x.log(ref, "read")
	x.exec("UPDATE record SET superseded_at=now() WHERE id=$1", x.record)
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) == 0 {
		t.Fatal(e, p)
	}
}
func TestReplayedAnswerAccepted(t *testing.T) {
	x := runningTurn(t)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, e := x.submit(t.Context(), "gap"); e != nil || len(p) > 0 {
				t.Error(e, p)
			}
		}()
	}
	wg.Wait()
	x.exec("UPDATE agent_run SET status='completed' WHERE id=$1", x.run.ID)
	if p, e := x.submit(t.Context(), "fact"); e != nil || len(p) > 0 {
		t.Fatal(e, p)
	}
	var n int
	if e := x.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_message WHERE agent_run_id=$1 AND kind='answer'", x.run.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal(e, n)
	}
}
func TestAnswerRequiresCurrentRequester(t *testing.T) {
	x := runningTurn(t)
	x.exec("DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", x.ws, x.user)
	if _, e := x.submit(t.Context(), "gap"); !errors.Is(e, service.ErrNotYours) {
		t.Fatal(e)
	}
}
func TestFailedRunLeavesFailureMessage(t *testing.T) {
	x := runningTurn(t)
	x.exec("UPDATE agent_run SET status='failed',error='private question',failure_reason='hand_in_invalid' WHERE id=$1", x.run.ID)
	x.run.Status = "failed"
	a := Answers{Store: x.runs.Store}
	for range 2 {
		if e := a.OnSettlement(t.Context(), service.Settlement{ID: x.run.ID, AgentRun: x.run}); e != nil {
			t.Fatal(e)
		}
	}
	var body string
	var n int
	if e := x.env.Owner.QueryRow(t.Context(), "SELECT count(*),min(body) FROM lab_message WHERE agent_run_id=$1 AND kind='failure'", x.run.ID).Scan(&n, &body); e != nil || n != 1 || !strings.HasPrefix(body, "Aristotle could not answer: ") || strings.Contains(body, "private question") {
		t.Fatal(e, n, body)
	}
}

// Hold a revocation open until the hand-in is known to be waiting on it.
func TestAnswerDoesNotRacePastMembershipRemoval(t *testing.T) {
	x := runningTurn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	removal, err := x.env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer removal.Rollback(ctx)
	var pid int
	if err = removal.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = removal.Exec(ctx, "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", x.ws, x.user); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := x.submit(ctx, "gap"); done <- e }()
	for {
		var waiting bool
		if err = x.env.Owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("hand-in did not wait for removal: %v", e)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = removal.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if e := <-done; !errors.Is(e, service.ErrNotYours) {
		t.Fatalf("hand-in after removal: %v", e)
	}
	var n int
	if e := x.env.Owner.QueryRow(ctx, "SELECT count(*) FROM lab_message WHERE agent_run_id=$1 AND kind='answer'", x.run.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal(e, n)
	}
}

func TestAnswerCannotBorrowAnotherRunsReads(t *testing.T) {
	x := runningTurn(t)
	chat := db.LabChat{ID: *x.run.LabChatID, OwnerID: x.user, WorkspaceID: x.ws}
	second := enqueueQuestion(t, x.fixture, x.runs, chat, "another turn", "")
	ref := Ref{"record", x.record}.String()
	x.exec("INSERT INTO lab_read(workspace_id,agent_run_id,ref,via) VALUES($1,$2,$3,'read')", x.ws, second.ID, ref)
	if p, e := x.submit(t.Context(), "fact", ref); e != nil || len(p) == 0 {
		t.Fatal(e, p)
	}
}

func TestAnswerLessonMustBeCheckedAndActive(t *testing.T) {
	x := runningTurn(t)
	id := uuid.New()
	ref := Ref{"lesson", id}.String()
	x.exec("INSERT INTO lesson(id,workspace_id,kind,text,cites,model) VALUES($1,$2,'lab','Lesson',$3,'test')", id, x.ws, `[{"ref":"record:`+x.record.String()+`","version":"live"}]`)
	x.log(ref, "read")
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) == 0 {
		t.Fatal(e, p)
	}
	x.exec("UPDATE lesson SET checked_at=now(),status='set_aside' WHERE id=$1", id)
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) == 0 {
		t.Fatal(e, p)
	}
	x.exec("UPDATE lesson SET status='active' WHERE id=$1", id)
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) > 0 {
		t.Fatal(e, p)
	}
}

func TestAnswerReadLogAndDefinerAreRequesterOnly(t *testing.T) {
	x := runningTurn(t)
	r := Ref{"record", x.record}.String()
	x.log(r, "read")
	other := uuid.New()
	x.exec(`INSERT INTO "user"(id,email,name) VALUES($1,'peer@test','peer')`, other)
	x.exec("INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')", x.ws, other)
	ctx := t.Context()
	err := x.runs.Store.InWorkspace(ctx, x.ws, other, func(tx *store.Tx) error {
		logs, e := tx.Q.LabEvidenceLog(ctx, x.run.ID)
		if e != nil {
			return e
		}
		if len(logs) != 0 {
			t.Fatalf("peer saw reads: %v", logs)
		}
		_, e = tx.Q.RecordLabAnswer(ctx, db.RecordLabAnswerParams{RunID: x.run.ID, Answer: []byte(`{"statements":[],"evidence":[]}`)})
		if !errors.Is(e, pgx.ErrNoRows) {
			t.Fatalf("peer recorded answer: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = x.runs.Store.InWorkspace(ctx, x.ws, other, func(tx *store.Tx) error {
		return tx.Q.AppendLabRead(ctx, db.AppendLabReadParams{WorkspaceID: x.ws, AgentRunID: x.run.ID, Ref: r, Via: "read"})
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("peer could append to read log: %v", err)
	}
}

// Lose the report connection after the fail transaction and delivery lease
// commit. A fresh server must recover without the original daemon or callback.
func TestAnswerFailureRecoversAfterInterruptedDelivery(t *testing.T) {
	x := runningTurn(t)
	ctx := t.Context()
	reportCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	x.runs.OnSettlement = func(context.Context, service.Settlement) error { cancel(); return context.Canceled }
	run, e := x.runs.Fail(reportCtx, x.user, x.ref, x.run.ID, service.Report{FailureReason: agent.ReasonHandInInvalid, Error: "private question"})
	if e != nil || run.Status != "failed" {
		t.Fatal(e, run.Status)
	}
	var claimed bool
	if e = x.env.Owner.QueryRow(ctx, "SELECT delivered_at IS NULL AND delivery_id IS NOT NULL AND lease_expires_at>now() FROM agent_run_settlement WHERE agent_run_id=$1", x.run.ID).Scan(&claimed); e != nil || !claimed {
		t.Fatal(e, claimed)
	}
	fresh := &service.AgentRuns{Store: &store.Store{Pool: x.env.App}}
	fresh.OnSettlement = (Answers{Store: fresh.Store}).OnSettlement
	count := func(want int) {
		t.Helper()
		var n int
		if e := x.env.Owner.QueryRow(ctx, "SELECT count(*) FROM lab_message WHERE agent_run_id=$1 AND kind='failure'", x.run.ID).Scan(&n); e != nil || n != want {
			t.Fatal(e, n)
		}
	}
	if e = fresh.RecoverSettlements(ctx); e != nil {
		t.Fatal(e)
	}
	count(0)
	// Advance only the abandoned lease instead of waiting five wall-clock minutes.
	x.exec("UPDATE agent_run_settlement SET lease_expires_at=now()-interval '1 second' WHERE agent_run_id=$1", x.run.ID)
	for range 2 {
		if e = fresh.RecoverSettlements(ctx); e != nil {
			t.Fatal(e)
		}
	}
	count(1)
	var delivered bool
	if e = x.env.Owner.QueryRow(ctx, "SELECT delivered_at IS NOT NULL FROM agent_run_settlement WHERE agent_run_id=$1", x.run.ID).Scan(&delivered); e != nil || !delivered {
		t.Fatal(e, delivered)
	}
}
