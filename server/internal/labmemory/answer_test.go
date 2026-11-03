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
	claims, e := historicalClaim(t, f, runs, ref)
	if e != nil || len(claims) != 1 {
		t.Fatal(e, claims)
	}
	ref.Attempt = claims[0].AgentRun.Attempt
	if _, e = historicalStart(t.Context(), runs, f.user, ref, run.ID); e != nil {
		t.Fatal(e)
	}
	return &turn{f, runs, run, ref, handin.HandIns{Runs: runs, Aristotle: Answers{Store: runs.Store}}}
}
func (x *turn) log(ref, via string) {
	var version string
	r, err := ParseRef(ref)
	if err == nil {
		if fact, e := x.read(r); e == nil {
			version = pin(fact).Version
		}
	}
	x.exec("INSERT INTO lab_read(workspace_id,agent_run_id,ref,via,read_version,attempt) VALUES($1,$2,$3,$4,$5,$6)", x.ws, x.run.ID, ref, via, version, x.ref.Attempt)
}
func (x *turn) submit(ctx context.Context, kind string, cites ...string) ([]string, error) {
	if cites == nil {
		cites = []string{}
	}
	raw, _ := json.Marshal(map[string]any{"statements": []any{handin.Statement{Kind: kind, Text: "Checked.", Cites: cites}}})
	return x.historicalAnswer(ctx, raw)
}

// Reproduce validation of a stored historical attempt. The public hand-in
// route now cancels these attempts and cannot ask a daemon to re-run them.
func (x *turn) historicalAnswer(ctx context.Context, raw json.RawMessage) ([]string, error) {
	var problems []string
	err := x.runs.OnAttempt(ctx, x.user, x.ref, x.run.ID, func(tx *store.Tx, run db.AgentRun) error {
		var err error
		problems, err = x.h.Aristotle.Answer(ctx, tx, run, raw)
		return err
	})
	return problems, err
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
	x.exec("INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','Lesson',$3,'test',now())", id, x.ws, `[{"ref":"record:`+x.record.String()+`","version":"live"}]`)
	x.log(ref, "read")
	x.exec("UPDATE lesson SET checked_at=NULL WHERE id=$1", id)
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) == 0 {
		t.Fatal(e, p)
	}
	x.exec("UPDATE lesson SET checked_at=now(),status='set_aside' WHERE id=$1", id)
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) == 0 {
		t.Fatal(e, p)
	}
	x.exec("UPDATE lesson SET status='active' WHERE id=$1", id)
	if p, e := x.submit(t.Context(), "lesson", ref); e != nil || len(p) != 1 || p[0] != FreshTurnProblem {
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
	fresh := &service.AgentRuns{Store: &store.Store{LessonsEnabled: true, Pool: x.env.App}}
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

func TestCompleteNeedsValidatedAnswer(t *testing.T) {
	x := runningTurn(t)
	if _, e := x.runs.Complete(t.Context(), x.user, x.ref, x.run.ID, service.Report{}); !errors.Is(e, service.ErrConflict) {
		t.Fatalf("completion without answer: %v", e)
	}
	if p, e := x.submit(t.Context(), "gap"); e != nil || len(p) > 0 {
		t.Fatal(e, p)
	}
	for range 2 {
		r, e := x.runs.Complete(t.Context(), x.user, x.ref, x.run.ID, service.Report{})
		if e != nil || r.Status != "completed" {
			t.Fatal(e, r.Status)
		}
	}
}
func TestAcceptedAnswerSurvivesFailureAndSweep(t *testing.T) {
	for _, mode := range []string{"failure", "sweep"} {
		t.Run(mode, func(t *testing.T) {
			x := runningTurn(t)
			ctx := t.Context()
			x.runs.OnSettlement = (Answers{Store: x.runs.Store}).OnSettlement
			if p, e := x.submit(ctx, "gap"); e != nil || len(p) > 0 {
				t.Fatal(e, p)
			}
			if mode == "failure" {
				for range 2 {
					r, e := x.runs.Fail(ctx, x.user, x.ref, x.run.ID, service.Report{FailureReason: agent.ReasonRuntimeOffline, Error: "crash"})
					if e != nil || r.Status != "completed" {
						t.Fatal(e, r.Status)
					}
				}
			} else {
				x.exec("UPDATE runtime SET status='offline' WHERE id=$1", x.ref.RuntimeID)
				for range 2 {
					if e := x.runs.Sweep(ctx); e != nil {
						t.Fatal(e)
					}
				}
			}
			var status string
			var outcomes int
			if e := x.env.Owner.QueryRow(ctx, "SELECT status,(SELECT count(*) FROM lab_message WHERE agent_run_id=r.id AND kind IN ('answer','failure')) FROM agent_run r WHERE id=$1", x.run.ID).Scan(&status, &outcomes); e != nil || status != "completed" || outcomes != 1 {
				t.Fatal(e, status, outcomes)
			}
		})
	}
}

func TestLaunchRechecksPrivateHistoryAfterClaim(t *testing.T) {
	for _, change := range []string{"unchanged", "lesson set aside", "team unshared", "lessons disabled"} {
		t.Run(change, func(t *testing.T) {
			x := runningTurn(t)
			ctx := t.Context()
			var r Ref
			var hide func()
			if strings.HasPrefix(change, "team unshared") {
				team := x.team()
				x.exec("UPDATE field_pack SET visibility='workspace' WHERE id=$1", team)
				r = Ref{"study", x.study}
				hide = func() { x.exec("UPDATE field_pack SET visibility='private' WHERE id=$1", team) }
			} else {
				r = x.lessonFor(Ref{"record", x.record})
				hide = func() { x.exec("UPDATE lesson SET status='set_aside' WHERE id=$1", r.ID) }
				if change == "lessons disabled" {
					hide = func() { x.runs.Store = &store.Store{Pool: x.env.App} }
				}
			}
			kind := "fact"
			if r.Kind == "lesson" {
				kind = "lesson"
			}
			x.log(r.String(), "read")
			raw, _ := json.Marshal(map[string]any{"statements": []any{handin.Statement{Kind: kind, Text: "sensitive-previous-answer", Cites: []string{r.String()}}}})
			if p, e := x.historicalAnswer(ctx, raw); e != nil || len(p) > 0 {
				t.Fatal(e, p)
			}
			if _, e := x.runs.Complete(ctx, x.user, x.ref, x.run.ID, service.Report{}); e != nil {
				t.Fatal(e)
			}
			chat := db.LabChat{ID: *x.run.LabChatID, WorkspaceID: x.ws, OwnerID: x.user}
			next := enqueueQuestion(t, x.fixture, x.runs, chat, "Follow up", "")
			claims, e := historicalClaim(t, x.fixture, x.runs, x.ref)
			if e != nil || len(claims) != 1 || claims[0].AgentRun.ID != next.ID {
				t.Fatal(e, claims)
			}
			claim := claims[0].AgentRun
			if strings.Contains(claim.Prompt, "sensitive-previous-answer") || claim.SessionID != "" {
				t.Fatal("claim retained private prior turn", claim.Prompt)
			}
			x.exec("UPDATE agent_run SET prompt='legacy-private-prompt' WHERE id=$1", claim.ID)
			if change != "unchanged" {
				hide()
			}
			runtime := x.ref
			runtime.Attempt = claim.Attempt
			prompt, e := historicalStart(ctx, x.runs, x.user, runtime, claim.ID)
			if e != nil || strings.Contains(prompt, "legacy-private-prompt") || !strings.Contains(prompt, "Follow up") || !strings.Contains(prompt, "private question") || strings.Contains(prompt, "sensitive-previous-answer") != (change == "unchanged") {
				t.Fatal(e, prompt)
			}
		})
	}
}

func TestConcurrentFailureAndHandInPublishOneOutcome(t *testing.T) {
	x := runningTurn(t)
	ctx := t.Context()
	x.runs.OnSettlement = (Answers{Store: x.runs.Store}).OnSettlement
	start := make(chan struct{})
	handed := make(chan error, 1)
	reported := make(chan error, 1)
	go func() {
		<-start
		p, e := x.submit(ctx, "gap")
		if len(p) != 0 {
			e = errors.New("invalid gap")
		}
		handed <- e
	}()
	go func() {
		<-start
		_, e := x.runs.Fail(ctx, x.user, x.ref, x.run.ID, service.Report{FailureReason: agent.ReasonHandInInvalid, Error: "fixture failure"})
		reported <- e
	}()
	close(start)
	submitErr, failErr := <-handed, <-reported
	if failErr != nil || submitErr != nil && !errors.Is(submitErr, service.ErrConflict) {
		t.Fatal(submitErr, failErr)
	}
	var status, kind string
	var n, events int
	if e := x.env.Owner.QueryRow(ctx, `SELECT status,(SELECT min(kind) FROM lab_message WHERE agent_run_id=r.id AND kind<>'question'),
 (SELECT count(*) FROM lab_message WHERE agent_run_id=r.id AND kind<>'question'),
 (SELECT count(*) FROM event WHERE type='lab.message.created' AND payload->>'message_id' IN
 (SELECT id::text FROM lab_message WHERE agent_run_id=r.id AND kind<>'question')) FROM agent_run r WHERE id=$1`, x.run.ID).Scan(&status, &kind, &n, &events); e != nil {
		t.Fatal(e)
	}
	if n != 1 || events != 1 || submitErr == nil && (kind != "answer" || status != "completed") || submitErr != nil && (kind != "failure" || status != "failed") {
		t.Fatal(submitErr, status, kind, n, events)
	}
}

func TestUnvalidatedStoredTextCannotAuthorizeCompletion(t *testing.T) {
	x := runningTurn(t)
	ctx := t.Context()
	x.exec("INSERT INTO lab_message(workspace_id,owner_id,chat_id,agent_run_id,kind,body) VALUES($1,$2,$3,$4,'answer','legacy answer')", x.ws, x.user, *x.run.LabChatID, x.run.ID)
	if _, e := x.runs.Complete(ctx, x.user, x.ref, x.run.ID, service.Report{}); !errors.Is(e, service.ErrConflict) {
		t.Fatal(e)
	}
}

func TestLaunchDropsOnlyAnswersThatSawRedactedHistory(t *testing.T) {
	x := runningTurn(t)
	ctx := t.Context()
	next := func(question string) string {
		t.Helper()
		if _, e := x.runs.Complete(ctx, x.user, x.ref, x.run.ID, service.Report{}); e != nil {
			t.Fatal(e)
		}
		x.run = enqueueQuestion(t, x.fixture, x.runs, db.LabChat{ID: *x.run.LabChatID, WorkspaceID: x.ws, OwnerID: x.user}, question, "")
		claims, e := historicalClaim(t, x.fixture, x.runs, x.ref)
		if e != nil || len(claims) != 1 {
			t.Fatal(e, claims)
		}
		x.ref.Attempt = claims[0].AgentRun.Attempt
		prompt, e := historicalStart(ctx, x.runs, x.user, x.ref, x.run.ID)
		if e != nil {
			t.Fatal(e)
		}
		return prompt
	}
	say := func(kind, body string, cites ...string) {
		t.Helper()
		if cites == nil {
			cites = []string{}
		}
		raw, _ := json.Marshal(map[string]any{"statements": []any{handin.Statement{Kind: kind, Text: body, Cites: cites}}})
		if p, e := x.historicalAnswer(ctx, raw); e != nil || len(p) > 0 {
			t.Fatal(e, p)
		}
	}
	lesson := x.lessonFor(Ref{"record", x.record})
	x.log(lesson.String(), "read")
	say("lesson", "sensitive lesson", lesson.String())
	if prompt := next("Second question"); !strings.Contains(prompt, "sensitive lesson") {
		t.Fatal(prompt)
	}
	say("gap", "dependent restatement")
	x.exec("UPDATE lesson SET status='set_aside' WHERE id=$1", lesson.ID)
	if prompt := next("Third question"); strings.Contains(prompt, "sensitive lesson") || strings.Contains(prompt, "dependent restatement") {
		t.Fatal(prompt)
	}
	say("gap", "new independent result")
	if prompt := next("Fourth question"); !strings.Contains(prompt, "new independent result") || strings.Contains(prompt, "dependent restatement") || strings.Contains(prompt, "sensitive lesson") {
		t.Fatal(prompt)
	}
}

func TestRevokedEvidenceCannotReachAnswer(t *testing.T) {
	for _, source := range []string{"tool read", "launch history"} {
		t.Run(source, func(t *testing.T) {
			x := runningTurn(t)
			ctx := t.Context()
			team := x.team()
			x.exec("UPDATE field_pack SET visibility='workspace' WHERE id=$1", team)
			ref := Ref{"study", x.study}
			x.log(ref.String(), "read")
			raw := func(k, text string, cites ...string) []byte {
				if cites == nil {
					cites = []string{}
				}
				b, _ := json.Marshal(map[string]any{"statements": []any{handin.Statement{Kind: k, Text: text, Cites: cites}}})
				return b
			}
			if source == "launch history" {
				if p, e := x.historicalAnswer(ctx, raw("fact", "private-method", ref.String())); e != nil || len(p) > 0 {
					t.Fatal(e, p)
				}
				if _, e := x.runs.Complete(ctx, x.user, x.ref, x.run.ID, service.Report{}); e != nil {
					t.Fatal(e)
				}
				x.run = enqueueQuestion(t, x.fixture, x.runs, db.LabChat{ID: *x.run.LabChatID, WorkspaceID: x.ws, OwnerID: x.user}, "Follow up", "")
				cs, e := historicalClaim(t, x.fixture, x.runs, x.ref)
				if e != nil || len(cs) != 1 {
					t.Fatal(e, cs)
				}
				x.ref.Attempt = cs[0].AgentRun.Attempt
				prompt, e := historicalStart(ctx, x.runs, x.user, x.ref, x.run.ID)
				if e != nil || !strings.Contains(prompt, "private-method") {
					t.Fatal(e, prompt)
				}
			}
			x.exec("UPDATE field_pack SET visibility='private' WHERE id=$1", team)
			for _, claim := range [][]byte{raw("gap", "private-method"), raw("fact", "private-method", ref.String())} {
				p, e := x.historicalAnswer(ctx, claim)
				if e != nil || len(p) == 0 {
					t.Fatalf("accepted revoked private content (%s): %v %v", source, e, p)
				}
			}
			// Reading the narrower projection cannot remove information from
			// the existing model session, and dropping every cite cannot help.
			x.log(ref.String(), "read")
			if p, e := x.historicalAnswer(ctx, raw("gap", "private-method")); e != nil || len(p) != 1 || p[0] != FreshTurnProblem {
				t.Fatal(e, p)
			}
			e := x.runs.Store.InWorkspace(ctx, x.ws, x.user, func(tx *store.Tx) error {
				_, err := tx.Q.RecordLabAnswer(ctx, db.RecordLabAnswerParams{RunID: x.run.ID, Answer: []byte(`{"statements":[],"evidence":[]}`)})
				return err
			})
			if !errors.Is(e, pgx.ErrNoRows) {
				t.Fatalf("recorder admitted a stale session: %v", e)
			}
			var n int
			if e := x.env.Owner.QueryRow(ctx, "SELECT count(*) FROM lab_message WHERE kind='answer' AND agent_run_id=$1", x.run.ID).Scan(&n); e != nil || n != 0 {
				t.Fatal(e, n)
			}
			oldAttempt := x.ref.Attempt
			retried, e := x.runs.Fail(ctx, x.user, x.ref, x.run.ID, service.Report{FailureReason: agent.ReasonLabContextChanged, Error: FreshTurnProblem})
			if e != nil || retried.Status != "queued" {
				t.Fatal(e, retried.Status)
			}
			claims, e := historicalClaim(t, x.fixture, x.runs, x.ref)
			if e != nil || len(claims) != 1 || claims[0].AgentRun.Attempt != oldAttempt+1 || claims[0].AgentRun.SessionID != "" {
				t.Fatal(e, claims)
			}
			x.ref.Attempt = claims[0].AgentRun.Attempt
			prompt, e := historicalStart(ctx, x.runs, x.user, x.ref, x.run.ID)
			if e != nil || strings.Contains(prompt, "private-method") {
				t.Fatal(e, prompt)
			}
			if p, e := x.historicalAnswer(ctx, raw("fact", "Updated projection", ref.String())); e != nil || len(p) == 0 || p[0] == FreshTurnProblem {
				t.Fatal("old reads authorized a new attempt", e, p)
			}
			x.log(ref.String(), "read")
			if p, e := x.historicalAnswer(ctx, raw("fact", "Updated projection", ref.String())); e != nil || len(p) > 0 {
				t.Fatal(e, p)
			}

		})
	}
}

func TestDaemonCannotRequestUnverifiedContextRestart(t *testing.T) {
	x := runningTurn(t)
	if _, e := x.runs.Fail(t.Context(), x.user, x.ref, x.run.ID, service.Report{FailureReason: agent.ReasonLabContextChanged, Error: FreshTurnProblem}); !errors.Is(e, service.ErrBadReport) {
		t.Fatal(e)
	}
}

func TestFactsOnlyRestartRejectsHandInFromEarlierLessonRead(t *testing.T) {
	x := runningTurn(t)
	lesson := x.lessonFor(Ref{"record", x.record})
	x.log(lesson.String(), "read")
	x.runs.Store = &store.Store{Pool: x.env.App}
	if p, err := x.submit(t.Context(), "lesson", lesson.String()); err != nil || len(p) != 1 || p[0] != FreshTurnProblem {
		t.Fatal(p, err)
	}
}
