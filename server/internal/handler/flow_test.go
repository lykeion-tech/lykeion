package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

// teamLab is a study on a two-member Team in Hypotheses, with its owner's
// daemon registered to claim the study's runs.
type teamLab struct {
	s             *server
	jwt, ws       string
	card          card
	personal, rts string
}

type teamRun struct {
	token, id, role, prompt, skill, scope string
	ref                                   map[string]any
	callToken                             string // the door's, which alone records calls
}

func newTeamLab(t *testing.T, steps int) *teamLab {
	t.Helper()
	return newTeamLabWith(t, testutil.PackOpts{Name: "hyp", Stage: "hypotheses", Members: 2, Steps: steps})
}

// newTeamLabWith is newTeamLab on a Team built from o, working Hypotheses.
func newTeamLabWith(t *testing.T, o testutil.PackOpts) *teamLab {
	t.Helper()
	s := newServer(t)
	l := &teamLab{s: s, jwt: s.signIn(t, "pi@lab.org")}
	l.ws = s.workspace(t, l.jwt, "lab").ID
	var up struct{ ID string }
	s.installPackFixture(t, l.jwt, l.ws, testutil.Pack(t, o), &up)
	if c := s.call(t, "POST", "/api/teams/"+up.ID+"/add", l.jwt, l.ws, nil, nil); c != 200 {
		t.Fatalf("add: %d", c)
	}
	l.personal = s.personalToken(t, l.jwt)
	var rts []struct{ ID string }
	if c := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": l.ws, "daemon_id": "d1", "harnesses": []string{"claude"}}, &rts); c != 200 || len(rts) != 1 {
		t.Fatalf("register %d %+v", c, rts)
	}
	l.rts = rts[0].ID
	if c := s.call(t, "POST", "/api/cards", l.jwt, l.ws, map[string]any{"question": "Why do cells age?",
		"assignee": teamAssignee(map[string]string{"hypotheses": up.ID})}, &l.card); c != 201 {
		t.Fatalf("card: %d", c)
	}
	return l
}

// claim waits for the study's next queued run, claims and starts it.
func (l *teamLab) claim(t *testing.T) teamRun {
	t.Helper()
	ref := map[string]any{"workspace_id": l.ws, "runtime_id": l.rts}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		var claims []struct {
			AgentRun struct {
				ID, Role, Prompt, Skill string
				Attempt                 int32
				ContinuityScope         string `json:"continuity_scope"`
			} `json:"agent_run"`
			Token     string
			CallToken string `json:"call_token"`
		}
		if c := l.s.call(t, "POST", "/api/daemon/agent-runs/claim", l.personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); c != 200 {
			t.Fatalf("claim %d", c)
		}
		if len(claims) == 0 {
			continue
		}
		a := claims[0].AgentRun
		ref["attempt"] = a.Attempt
		if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+a.ID+"/start", l.personal, "", ref, nil); c != 204 {
			t.Fatalf("start %d", c)
		}
		return teamRun{claims[0].Token, a.ID, a.Role, a.Prompt, a.Skill, a.ContinuityScope, ref, claims[0].CallToken}
	}
	t.Fatal("no run was queued")
	return teamRun{}
}

func (l *teamLab) complete(t *testing.T, r teamRun) {
	t.Helper()
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+r.id+"/complete", l.personal, "", r.ref, nil); c != 200 {
		t.Fatalf("complete %d", c)
	}
}

func (l *teamLab) queued(t *testing.T) int {
	return l.s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND status = 'queued'", l.card.ID)
}

type stepOut struct {
	Key        string
	AgentRunID string `json:"agent_run_id"`
	Error      string
}

func (l *teamLab) step(t *testing.T, tok string, in map[string]any) (int, stepOut) {
	t.Helper()
	var out stepOut
	c := l.s.call(t, "POST", "/api/role/steps", tok, "", in, &out)
	return c, out
}

func TestALeaderNamesADeclaredMember(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	if leader.role != "leader" {
		t.Fatalf("first run %q", leader.role)
	}
	c, out := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "List three rival explanations."})
	if c != 201 || out.Key == "" || out.AgentRunID == "" {
		t.Fatalf("step %d %+v", c, out)
	}
	var recs []struct {
		Kind, Key, Role string
		Body            map[string]any
	}
	if c := l.s.call(t, "GET", "/api/role/records", leader.token, "", nil, &recs); c != 200 || len(recs) != 1 {
		t.Fatalf("records %d %+v", c, recs)
	}
	r := recs[0]
	if r.Kind != "delegation" || r.Key != out.Key || r.Role != "leader" || r.Body["member"] != "member-1" ||
		r.Body["step"] != "List three rival explanations." || r.Body["agent_run_id"] != out.AgentRunID {
		t.Fatalf("delegation %+v", r)
	}
	m := l.claim(t)
	if m.id != out.AgentRunID || m.role != "member-1" || !strings.Contains(m.prompt, "List three rival explanations.") {
		t.Fatalf("member run %+v", m)
	}
	if !strings.HasPrefix(m.skill, "You are member-1.") || !strings.Contains(m.skill, "lyk record put") {
		t.Fatalf("member skill:\n%s", m.skill)
	}
	if want := flow.ContinuityScope(uuid.MustParse(l.card.ID), "member-1", "hypotheses", 1); m.scope != want {
		t.Fatalf("scope %q, want %q", m.scope, want)
	}
}

func TestAnUndeclaredMemberIsRefused(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	for _, name := range []string{"ghost", "pi", "auditor", ""} {
		if c, out := l.step(t, leader.token, map[string]any{"member": name, "content": "x"}); c != 400 {
			t.Fatalf("%q: %d %+v", name, c, out)
		}
	}
	if n := l.queued(t); n != 0 {
		t.Fatalf("%d runs queued", n)
	}
}

func TestAtTheCapTheLeaderCanStillFinish(t *testing.T) {
	l := newTeamLab(t, 1)
	leader := l.claim(t)
	c, first := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	if c != 201 {
		t.Fatalf("first step %d", c)
	}
	c, out := l.step(t, leader.token, map[string]any{"member": "member-2", "content": "two"})
	if c != 409 || out.Error != "step cap reached: finish with lyk team done or do the step yourself" {
		t.Fatalf("over the cap: %d %+v", c, out)
	}
	if c, out := l.step(t, leader.token, map[string]any{"self": true, "content": "I compare the rivals myself."}); c != 201 || out.Key == "" {
		t.Fatalf("self step %d %+v", c, out)
	}
	l.complete(t, l.claim(t)) // the member's run
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Done.", "cites": []string{"r99"}}, nil); c != 400 {
		t.Fatalf("unknown cite: %d", c)
	}
	var done struct{ Key string }
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Two rivals stand.", "cites": []string{first.Key}}, &done); c != 201 || done.Key == "" {
		t.Fatalf("done %d %+v", c, done)
	}
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'summary' AND body->>'text' = 'Two rivals stand.' AND body->'cites'->>0 = $2", l.card.ID, first.Key); n != 1 {
		t.Fatalf("%d summaries", n)
	}
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'self_step' AND body->>'step' = 'I compare the rivals myself.'", l.card.ID); n != 1 {
		t.Fatalf("%d self steps", n)
	}
}

func TestDoneWaitsForInFlightWork(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	if c, _ := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"}); c != 201 {
		t.Fatalf("step %d", c)
	}
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Too soon."}, nil); c != 409 {
		t.Fatalf("done with a member in flight: %d", c)
	}
	m := l.claim(t)
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Too soon."}, nil); c != 409 {
		t.Fatalf("done with a member running: %d", c)
	}
	l.complete(t, m)
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Now."}, nil); c != 201 {
		t.Fatalf("done after the member: %d", c)
	}
}

func TestMembersCannotWriteServerKinds(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	m := l.claim(t)
	for _, kind := range []string{"score", "delegation", "self_step", "summary", "audit", "outcome", "call", "tools", "Bad Kind", "1note"} {
		if c := l.s.call(t, "POST", "/api/role/records", m.token, "", map[string]any{"kind": kind, "body": map[string]any{}}, nil); c != 400 {
			t.Fatalf("%s: %d", kind, c)
		}
	}
	if c := l.s.call(t, "POST", "/api/role/records", m.token, "", map[string]any{"kind": "rival", "body": map[string]any{"cites": []string{"call:c1"}}}, nil); c != 400 {
		t.Fatalf("an unrecorded call cited: %d", c)
	}
	var rec struct{ Key string }
	if c := l.s.call(t, "POST", "/api/role/records", m.token, "", map[string]any{"kind": "rival", "body": map[string]any{"text": "a batch effect"}}, &rec); c != 201 || rec.Key == "" {
		t.Fatalf("work record %d %+v", c, rec)
	}
	// The leader writes work when it does a step itself.
	if c := l.s.call(t, "POST", "/api/role/records", leader.token, "", map[string]any{"kind": "comparison", "body": map[string]any{}}, nil); c != 201 {
		t.Fatalf("leader work record: %d", c)
	}
}

func TestAMemberRunSettlingWakesTheLeader(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	l.complete(t, leader) // the leader stops after naming a member
	m := l.claim(t)
	var rec struct{ Key string }
	if c := l.s.call(t, "POST", "/api/role/records", m.token, "", map[string]any{"kind": "rival", "body": map[string]any{"text": "a batch effect"}}, &rec); c != 201 {
		t.Fatalf("record %d", c)
	}
	if n := l.queued(t); n != 0 {
		t.Fatalf("the leader woke before the member finished: %d queued", n)
	}
	l.complete(t, m)
	again := l.claim(t)
	if again.role != "leader" || !strings.Contains(again.prompt, "member-1") || !strings.Contains(again.prompt, rec.Key) || !strings.Contains(again.prompt, "a batch effect") {
		t.Fatalf("leader resume %+v", again)
	}
	if again.scope != leader.scope {
		t.Fatalf("the leader resumed on %q, not its session %q", again.scope, leader.scope)
	}
}

func TestAFailedMemberWakesTheLeaderWithItsReason(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-2", "content": "two"})
	l.complete(t, leader)
	m := l.claim(t)
	if c := l.s.call(t, "POST", "/api/role/records", m.token, "", map[string]any{"kind": "failed", "body": map[string]any{"reason": "GEO has no aged mice"}}, nil); c != 201 {
		t.Fatalf("failed %d", c)
	}
	l.complete(t, m)
	again := l.claim(t)
	if again.role != "leader" || !strings.Contains(again.prompt, "member-2") || !strings.Contains(again.prompt, "GEO has no aged mice") {
		t.Fatalf("leader resume %+v", again)
	}
}

func TestALeaderIdleTwiceEscalatesOnce(t *testing.T) {
	l := newTeamLab(t, 0)
	l.complete(t, l.claim(t)) // stops without naming a member or finishing
	nudged := l.claim(t)
	if nudged.role != "leader" || !strings.Contains(nudged.prompt, "You ended without naming a member (`lyk team step`) or declaring the column done (`lyk team done`).") {
		t.Fatalf("nudge %+v", nudged)
	}
	l.complete(t, nudged)
	if n := l.queued(t); n != 0 {
		t.Fatalf("woken a third time: %d queued", n)
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'escalated'", l.card.ID); n != 1 {
		t.Fatalf("%d escalated items", n)
	}
	if n := l.s.count(t, "SELECT count(*) FROM card WHERE id = $1 AND state = 'working'", l.card.ID); n != 1 {
		t.Fatal("the study left working")
	}
	if c := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "Try the GTEx data."}, nil); c != 201 {
		t.Fatalf("comment %d", c)
	}
	if woken := l.claim(t); woken.role != "leader" || !strings.Contains(woken.prompt, "Try the GTEx data.") {
		t.Fatalf("comment wake %+v", woken)
	}
}

func TestOnlyTheLeaderNamesMembers(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	m := l.claim(t)
	if c, _ := l.step(t, m.token, map[string]any{"member": "member-2", "content": "two"}); c != 403 {
		t.Fatalf("member steps: %d", c)
	}
	if c, _ := l.step(t, m.token, map[string]any{"self": true, "content": "two"}); c != 403 {
		t.Fatalf("member self-steps: %d", c)
	}
	if c := l.s.call(t, "POST", "/api/role/done", m.token, "", map[string]any{"summary": "x"}, nil); c != 403 {
		t.Fatalf("member done: %d", c)
	}
}

func TestACancelledMemberWakesTheLeader(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	_, out := l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	l.complete(t, leader)
	if c := l.s.call(t, "POST", "/api/agent-runs/"+out.AgentRunID+"/cancel", l.jwt, l.ws, nil, nil); c != 204 {
		t.Fatalf("cancel %d", c)
	}
	again := l.claim(t)
	if again.role != "leader" || !strings.Contains(again.prompt, "member-1's run was cancelled") {
		t.Fatalf("leader resume %+v", again)
	}
}

func TestADeclaredDoneColumnTakesNoMoreSteps(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	l.complete(t, l.claim(t)) // the member lands while the leader still runs, which queues a wake
	if n := l.queued(t); n != 1 {
		t.Fatalf("%d leader wakes queued", n)
	}
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Done."}, nil); c != 201 {
		t.Fatalf("done %d", c)
	}
	if n := l.s.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'leader' AND status = 'queued'", l.card.ID); n != 0 {
		t.Fatalf("the wake queued before done is still queued: %d", n)
	}
	for _, in := range []map[string]any{{"member": "member-2", "content": "two"}, {"self": true, "content": "two"}} {
		if c, out := l.step(t, leader.token, in); c != 409 || out.Error != "this column was already declared done" {
			t.Fatalf("%v after done: %d %+v", in, c, out)
		}
	}
	if c := l.s.call(t, "POST", "/api/role/done", leader.token, "", map[string]any{"summary": "Again."}, nil); c != 409 {
		t.Fatalf("second done: %d", c)
	}
}

func TestAnIdleLeaderWaitsForItsMember(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	m := l.claim(t)
	l.complete(t, leader)
	l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "How is it going?"}, nil)
	l.complete(t, l.claim(t)) // woken by the comment, it stops idle while the member works
	if n := l.queued(t); n != 0 {
		t.Fatalf("an idle leader was nudged with its member at work: %d queued", n)
	}
	l.complete(t, m)
	if again := l.claim(t); again.role != "leader" || !strings.Contains(again.prompt, "member-1 finished") {
		t.Fatalf("leader resume %+v", again)
	}
}

func TestNoEscalationWhileTheLeaderIsQueued(t *testing.T) {
	l := newTeamLab(t, 0)
	l.complete(t, l.claim(t))
	nudged := l.claim(t)
	l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "Try GTEx."}, nil)
	for deadline := time.Now().Add(5 * time.Second); l.queued(t) == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
	}
	l.complete(t, nudged)
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'escalated'", l.card.ID); n != 0 {
		t.Fatalf("escalated with the leader queued: %d", n)
	}
}

func TestOnlyTheDaemonRecordsCalls(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	in := map[string]any{"server": "pubmed", "tool": "search", "answer": "hit"}
	if leader.callToken == "" || leader.callToken == leader.token {
		t.Fatalf("call token %q", leader.callToken)
	}
	// the agent's own token cannot record a call it made up
	if c := l.s.call(t, "POST", "/api/role/calls", leader.token, "", in, nil); c != 403 {
		t.Fatalf("run token: %d", c)
	}
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", in, nil); c != 201 {
		t.Fatalf("call token: %d", c)
	}
	// and the door's token does nothing else
	if c := l.s.call(t, "GET", "/api/role/records", leader.callToken, "", nil, nil); c != 403 {
		t.Fatalf("call token reading records: %d", c)
	}
}

func TestCallKeysAreSequential(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	for i, want := range []string{"c1", "c2", "c3"} {
		var out struct{ Key string }
		in := map[string]any{"server": "pubmed", "tool": "search", "args": map[string]any{"q": i}, "answer": "hit " + want}
		if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", in, &out); c != 201 || out.Key != want {
			t.Fatalf("call %d: %d %+v", i, c, out)
		}
	}
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", map[string]any{"server": "pubmed", "tool": "search"}, nil); c != 201 {
		t.Fatalf("empty answer: %d", c)
	}
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", map[string]any{"tool": "search", "answer": "x"}, nil); c != 400 {
		t.Fatalf("no server: %d", c)
	}
	var recs []db.Record
	if c := l.s.call(t, "GET", "/api/role/records?kind=call", leader.token, "", nil, &recs); c != 200 || len(recs) != 4 {
		t.Fatalf("records %d %d", c, len(recs))
	}
}

func TestALargeAnswerIsKeptWhole(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	// the last line sits far past the 256 KiB a body keeps
	answer := strings.Repeat("filler line of text\n", (1<<20)/20) + "the decisive sentence, 42 patients."
	var out struct{ Key string }
	in := map[string]any{"server": "pubmed", "tool": "fetch", "answer": answer}
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", in, &out); c != 201 || out.Key != "c1" {
		t.Fatalf("call %d %+v", c, out)
	}
	var recs []db.Record
	if c := l.s.call(t, "GET", "/api/role/records?kind=call", leader.token, "", nil, &recs); c != 200 || len(recs) != 1 {
		t.Fatalf("records %d", c)
	}
	var body struct {
		Answer       string
		AnswerSHA256 string `json:"answer_sha256"`
		Truncated    bool
	}
	json.Unmarshal(recs[0].Body, &body)
	if !body.Truncated {
		t.Fatal("a body that keeps part of the answer says it is not truncated")
	}
	if len(body.Answer) > 256<<10 || len(body.Answer) < 256<<10-4 || recs[0].BlobKey == "" {
		t.Fatalf("body keeps %d bytes, blob %q", len(body.Answer), recs[0].BlobKey)
	}
	calls, err := flow.LoadCalls(context.Background(), l.s.H.Blobs, recs)
	if err != nil || calls["c1"].Answer != answer {
		t.Fatalf("load: %v, %d bytes", err, len(calls["c1"].Answer))
	}
	if sum := sha256.Sum256([]byte(answer)); hex.EncodeToString(sum[:]) != body.AnswerSHA256 {
		t.Fatalf("answer_sha256 %s", body.AnswerSHA256)
	}
	quote := func(q string) bool {
		b, _ := json.Marshal(map[string]any{"cites": []string{"call:c1"}, "quote": q})
		return flow.MatchQuotes([]db.Record{{Key: "k", Body: b}}, calls)[0].Matched
	}
	if !quote("the decisive sentence,\n42 patients.") || quote("the decisive sentence, 43 patients.") {
		t.Fatal("matching must read the whole answer, word for word")
	}
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", map[string]any{"server": "s", "tool": "t", "answer": strings.Repeat("a", 32<<20+1)}, nil); c != 400 {
		t.Fatalf("over 32 MiB: %d", c)
	}
}

func TestACommentUnblocksIntoARedo(t *testing.T) {
	l := newTeamLab(t, 0)
	l.claim(t) // the first attempt's leader
	if _, err := l.s.Env.Owner.Exec(context.Background(), "UPDATE card SET state = 'blocked', block_kind = 'low_score', redos = 1 WHERE id = $1", l.card.ID); err != nil {
		t.Fatal(err)
	}
	const fix = "Cite the GEO accession for every claim."
	if c := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": fix}, nil); c != 201 {
		t.Fatalf("comment %d", c)
	}
	redo := l.claim(t)
	if redo.role != "leader" || !strings.Contains(redo.prompt, fix) || redo.scope != flow.ContinuityScope(uuid.MustParse(l.card.ID), "leader", "hypotheses", 2) {
		t.Fatalf("redo run: %s %s\n%s", redo.role, redo.scope, redo.prompt)
	}
	var state, kind string
	var round, redos int
	l.s.Env.Owner.QueryRow(context.Background(), "SELECT state, block_kind, round, redos FROM card WHERE id = $1", l.card.ID).Scan(&state, &kind, &round, &redos)
	if state != "working" || kind != "" || round != 2 || redos != 1 {
		t.Fatalf("after the comment: %s %q round %d redos %d", state, kind, round, redos)
	}
}

// The dashboard lifts the need out after "what it needed: ", so that text is pinned.
func TestNoToolText(t *testing.T) {
	want := "An agent on study #7's grounding column found no skill or connector that fits what it needed: a mass-spec parser"
	if got := noToolText(7, "grounding", "a mass-spec parser"); got != want {
		t.Fatalf("got %q", got)
	}
}

// newAgentLab is a study worked by one agent in Grounding, with no Team, and
// its owner's daemon registered to claim the study's runs.
func newAgentLab(t *testing.T) *teamLab {
	t.Helper()
	s := newServer(t)
	l := &teamLab{s: s, jwt: s.signIn(t, "pi@lab.org")}
	l.ws = s.workspace(t, l.jwt, "lab").ID
	var a struct{ ID string }
	if c := s.call(t, "POST", "/api/agents", l.jwt, l.ws, map[string]any{"name": "Curie", "harness": "claude", "visibility": "private"}, &a); c != 201 {
		t.Fatalf("agent: %d", c)
	}
	l.personal = s.personalToken(t, l.jwt)
	var rts []struct{ ID string }
	if c := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": l.ws, "daemon_id": "d1", "harnesses": []string{"claude"}}, &rts); c != 200 || len(rts) != 1 {
		t.Fatalf("register %d %+v", c, rts)
	}
	l.rts = rts[0].ID
	if c := s.call(t, "POST", "/api/cards", l.jwt, l.ws, map[string]any{"question": "Why do cells age?",
		"scope": map[string]string{"from": "grounding", "to": "grounding"}, "assignee": map[string]any{"type": "agent", "id": a.ID}}, &l.card); c != 201 {
		t.Fatalf("card: %d", c)
	}
	return l
}

func TestASingleAgentIdleTwiceEscalatesAndACommentWakesIt(t *testing.T) {
	l := newAgentLab(t)
	first := l.claim(t)
	if first.role != "agent" {
		t.Fatalf("first run %+v", first)
	}
	l.complete(t, first) // stops without declaring the column done
	nudged := l.claim(t)
	if nudged.role != "agent" || !strings.Contains(nudged.prompt, "You ended without declaring the column done (`lyk team done`).") {
		t.Fatalf("nudge %+v", nudged)
	}
	if nudged.scope != first.scope {
		t.Fatalf("the nudge started a new session %q, not %q", nudged.scope, first.scope)
	}
	l.complete(t, nudged)
	if n := l.queued(t); n != 0 {
		t.Fatalf("woken a third time: %d queued", n)
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'escalated' AND resolved_at IS NULL", l.card.ID); n != 1 {
		t.Fatalf("%d open escalated items", n)
	}
	if c := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "Try the GTEx data."}, nil); c != 201 {
		t.Fatalf("comment %d", c)
	}
	if woken := l.claim(t); woken.role != "agent" || !strings.Contains(woken.prompt, "Try the GTEx data.") {
		t.Fatalf("comment wake %+v", woken)
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'escalated' AND resolved_at IS NULL", l.card.ID); n != 0 {
		t.Fatalf("the comment that woke the agent left %d escalated items open", n)
	}
}

func TestAWakeThatFailsIsDeliveredAgain(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	l.step(t, leader.token, map[string]any{"member": "member-1", "content": "one"})
	l.complete(t, leader)
	m := l.claim(t)
	// The leader's agent cannot be read while the member settles, so waking
	// the leader fails.
	var pack, user, agent string
	if err := l.s.Env.Owner.QueryRow(context.Background(), "DELETE FROM team_agent WHERE member = 'pi' RETURNING pack_id::text, user_id::text, agent_id::text").Scan(&pack, &user, &agent); err != nil {
		t.Fatal(err)
	}
	l.complete(t, m)
	if n := l.queued(t); n != 0 {
		t.Fatalf("%d runs queued without the leader's agent", n)
	}
	if _, err := l.s.Env.Owner.Exec(context.Background(), "INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'pi', $4)", l.ws, pack, user, agent); err != nil {
		t.Fatal(err)
	}
	if err := l.s.H.AgentRuns.RecoverSettlements(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again := l.claim(t); again.role != "leader" || !strings.Contains(again.prompt, "member-1 finished") {
		t.Fatalf("leader resume %+v", again)
	}
}

// fail reports r as failed for reason, as a daemon does; a retried attempt
// comes back from the next claim with the same id.
func (l *teamLab) fail(t *testing.T, r teamRun, reason string) {
	t.Helper()
	ref := maps.Clone(r.ref)
	ref["failure_reason"] = reason
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+r.id+"/fail", l.personal, "", ref, nil); c != 200 {
		t.Fatalf("fail %d", c)
	}
}

// failUntilSettled fails the leader's run, and any retried attempt of it,
// until it ends for good, and returns the next run claimed after it.
func (l *teamLab) failUntilSettled(t *testing.T, r teamRun) teamRun {
	t.Helper()
	for {
		l.fail(t, r, "agent_error")
		next := l.claim(t)
		if next.id != r.id {
			return next
		}
		r = next
	}
}

func TestAFailedLeaderIsToldItFailedAndEscalatesAsFailed(t *testing.T) {
	l := newTeamLab(t, 0)
	woken := l.failUntilSettled(t, l.claim(t))
	if woken.role != "leader" || !strings.Contains(woken.prompt, "failed") || strings.Contains(woken.prompt, "You ended without") {
		t.Fatalf("the wake after a failed run: %+v", woken)
	}
	l.fail(t, woken, "agent_error")
	for n := l.queued(t); n > 0; n = l.queued(t) {
		woken = l.failUntilSettled(t, l.claim(t))
	}
	var text string
	l.s.Env.Owner.QueryRow(context.Background(), "SELECT text FROM inbox_item WHERE card_id = $1 AND kind = 'escalated'", l.card.ID).Scan(&text)
	if !strings.Contains(text, "failed") || strings.Contains(text, "stopped") {
		t.Fatalf("escalation %q", text)
	}
}
