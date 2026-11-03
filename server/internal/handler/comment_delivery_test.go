package handler

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type sentComment struct {
	ID, Author, Body string
}

type shownComment struct {
	ID       string
	Body     string
	Delivery *struct{ How, Role string }
}

func (l *teamLab) comment(t *testing.T, body string) string {
	t.Helper()
	var c struct{ ID string }
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": body}, &c); code != 201 {
		t.Fatalf("comment %d", code)
	}
	return c.ID
}

func (l *teamLab) runComments(t *testing.T, r teamRun) (int, []sentComment) {
	t.Helper()
	q := url.Values{"workspace_id": {l.ws}, "runtime_id": {l.rts}, "attempt": {fmt.Sprint(r.ref["attempt"])}}
	var out []sentComment
	code := l.s.call(t, "GET", "/api/daemon/agent-runs/"+r.id+"/comments?"+q.Encode(), l.personal, "", nil, &out)
	return code, out
}

func (l *teamLab) listComments(t *testing.T) map[string]shownComment {
	t.Helper()
	var out []shownComment
	if code := l.s.call(t, "GET", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, nil, &out); code != 200 {
		t.Fatalf("list comments %d", code)
	}
	m := map[string]shownComment{}
	for _, c := range out {
		m[c.ID] = c
	}
	return m
}

func (l *teamLab) receipts(t *testing.T, comment string) int {
	return l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'comment_delivery' AND body->>'comment_id' = $2", l.card.ID, comment)
}

// Two pollers of the same running turn race for a comment: one carries it,
// the other gets nothing, and the comment has one receipt.
func TestCommentClaimedOnce(t *testing.T) {
	l, leader := groundingLab(t)
	id := l.comment(t, "Prefer human cohorts.")
	if got := l.listComments(t)[id]; got.Delivery != nil {
		t.Fatalf("undelivered comment shows %+v", got.Delivery)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	got := make([][]sentComment, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i], got[i] = l.runComments(t, leader) }()
	}
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 || len(got[0])+len(got[1]) != 1 {
		t.Fatalf("claims %v %+v", codes, got)
	}
	c := append(got[0], got[1]...)[0]
	if c.ID != id || c.Body != "Prefer human cohorts." || c.Author == "" {
		t.Fatalf("claimed %+v", c)
	}
	if n := l.receipts(t, id); n != 1 {
		t.Fatalf("%d receipts", n)
	}
	if d := l.listComments(t)[id].Delivery; d == nil || d.How != "running_turn" || d.Role != "leader" {
		t.Fatalf("delivery %+v", d)
	}
	if code, again := l.runComments(t, leader); code != 200 || len(again) != 0 {
		t.Fatalf("second poll %d %+v", code, again)
	}
}

func TestCancelStudyCommentPreservesReceiptAndPreventsDelivery(t *testing.T) {
	l, run := groundingLab(t)
	cancelled := l.comment(t, "Do not deliver this.")
	path := "/api/cards/" + l.card.ID + "/comments/" + cancelled
	other := l.s.signIn(t, "other@lab.org")
	if _, err := l.s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, l.ws, l.s.userID(t, other)); err != nil {
		t.Fatal(err)
	}
	if code := l.s.call(t, "DELETE", path, other, l.ws, nil, nil); code != 404 {
		t.Fatalf("another member cancelled comment: %d", code)
	}
	if code := l.s.call(t, "DELETE", path, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	if code, comments := l.runComments(t, run); code != 200 || len(comments) != 0 {
		t.Fatalf("delivered cancelled comment: %d %+v", code, comments)
	}
	if _, ok := l.listComments(t)[cancelled]; ok {
		t.Fatal("cancelled comment still shown")
	}
	var cited []struct {
		ID, Body  string
		Cancelled bool
	}
	if code := l.s.call(t, "GET", "/api/cards/"+l.card.ID+"/comments?cite="+cancelled, other, l.ws, nil, &cited); code != 200 || len(cited) != 1 || cited[0].ID != cancelled || cited[0].Body != "Do not deliver this." || !cited[0].Cancelled {
		t.Fatalf("cancelled citation: %d %+v", code, cited)
	}
	if n := l.s.count(t, `SELECT count(*) FROM comment WHERE id=$1`, cancelled); n != 1 {
		t.Fatalf("lost comment provenance: %d", n)
	}
	delivered := l.comment(t, "Already delivered.")
	if code, comments := l.runComments(t, run); code != 200 || len(comments) != 1 {
		t.Fatalf("delivery: %d %+v", code, comments)
	}
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+delivered, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("cancel delivered: %d", code)
	}
	if n := l.receipts(t, delivered); n != 1 {
		t.Fatalf("lost delivery receipt: %d", n)
	}
}

func TestTargetedCommentGoesOnlyToSelectedRun(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	member := l.claim(t)
	if member.id != ask.Ticket {
		t.Fatal("wrong member run")
	}
	var comment struct{ ID string }
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws,
		map[string]any{"body": "For the member.", "target_run_id": member.id}, &comment); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	if code, got := l.runComments(t, leader); code != 200 || len(got) != 0 {
		t.Fatalf("wrong run received comment: %d %+v", code, got)
	}
	if code, got := l.runComments(t, member); code != 200 || len(got) != 1 || got[0].ID != comment.ID {
		t.Fatalf("target did not receive comment: %d %+v", code, got)
	}
}

func TestReplyToFinishedRunIsStudyComment(t *testing.T) {
	l, leader := groundingLab(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1", leader.id); err != nil {
		t.Fatal(err)
	}
	var comment struct{ ID string }
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws,
		map[string]any{"body": "A note on this work.", "target_run_id": leader.id}, &comment); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	var attempt *int32
	if err := l.s.Env.Owner.QueryRow(t.Context(), "SELECT steer_attempt FROM comment WHERE id = $1", comment.ID).Scan(&attempt); err != nil || attempt != nil {
		t.Fatalf("finished reply targeted an attempt: %v %v", attempt, err)
	}
}

func TestTargetedCommentDoesNotEnterAnotherRunsPrompt(t *testing.T) {
	l, leader := groundingLab(t)
	var comment struct{ ID string }
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws,
		map[string]any{"body": "Private steering for the lead.", "target_run_id": leader.id}, &comment); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	member := l.claim(t)
	if member.id != ask.Ticket || strings.Contains(member.prompt, "Private steering for the lead.") {
		t.Fatalf("targeted steering appeared in member prompt: %s", member.prompt)
	}
	if code, got := l.runComments(t, leader); code != 200 || len(got) != 1 || got[0].ID != comment.ID {
		t.Fatalf("target did not receive steering: %d %+v", code, got)
	}
}

func TestTargetedCommentIsNotRedirectedAfterRunEnds(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	member := l.claim(t)
	if member.id != ask.Ticket {
		t.Fatal("wrong member run")
	}
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws,
		map[string]any{"body": "For the member.", "target_run_id": member.id}, nil); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1", member.id); err != nil {
		t.Fatal(err)
	}
	if code, got := l.runComments(t, leader); code != 200 || len(got) != 0 {
		t.Fatalf("ended run's reply redirected: %d %+v", code, got)
	}
}

func TestReplyRejectsRunFromAnotherStudy(t *testing.T) {
	l, leader := groundingLab(t)
	var other string
	if err := l.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column)
		SELECT workspace_id, 900, 'Other', 'Other study?', owner_id, 'grounding' FROM card WHERE id = $1 RETURNING id`, l.card.ID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if code := l.s.call(t, "POST", "/api/cards/"+other+"/comments", l.jwt, l.ws,
		map[string]any{"body": "Not for this run.", "target_run_id": leader.id}, nil); code != 404 {
		t.Fatalf("cross-study target: got %d, want 404", code)
	}
}

func TestReplyToRunningCodexIsStudyComment(t *testing.T) {
	l, leader := groundingLab(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness = 'codex' WHERE id = $1", leader.id); err != nil {
		t.Fatal(err)
	}
	var comment struct{ ID string }
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws,
		map[string]any{"body": "A note for this study.", "target_run_id": leader.id}, &comment); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	if code, got := l.runComments(t, leader); code != 200 || len(got) != 0 {
		t.Fatalf("unsupported live input was claimed: %d %+v", code, got)
	}
	var attempt *int32
	if err := l.s.Env.Owner.QueryRow(t.Context(), "SELECT steer_attempt FROM comment WHERE id = $1", comment.ID).Scan(&attempt); err != nil || attempt != nil {
		t.Fatalf("codex reply targeted an unsupported live attempt: %v %v", attempt, err)
	}
}

// A Codex member can't take a comment mid-turn and its daemon never asks for
// one: the leader waiting on it gets the comment in its next member_result,
// once, and the study page says so.
func TestCodexMemberCommentGoesToLeader(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	l.claim(t)
	id := l.comment(t, "Skip mouse studies.")
	c, out := l.result(t, leader.token, ask.Ticket)
	if c != 200 || out.Status != "working" || len(out.Comments) != 1 {
		t.Fatalf("result %d %+v", c, out)
	}
	sent := out.Comments[0].(map[string]any)
	if sent["id"] != id || sent["body"] != "Skip mouse studies." {
		t.Fatalf("comment %+v", sent)
	}
	if d := l.listComments(t)[id].Delivery; d == nil || d.How != "member_result" || d.Role != "leader" {
		t.Fatalf("delivery %+v", d)
	}
	if _, again := l.result(t, leader.token, ask.Ticket); len(again.Comments) != 0 {
		t.Fatalf("delivered twice: %+v", again)
	}
	if n := l.receipts(t, id); n != 1 {
		t.Fatalf("%d receipts", n)
	}
}

// A comment no turn took yet goes into the next run's prompt, which keeps
// its receipt.
func TestCommentInNextPrompt(t *testing.T) {
	l, leader := groundingLab(t)
	id := l.comment(t, "Look at yeast too.")
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	m := l.claim(t)
	if m.id != ask.Ticket || !strings.Contains(m.prompt, "Look at yeast too.") {
		t.Fatalf("claimed %s:\n%s", m.id, m.prompt)
	}
	if d := l.listComments(t)[id].Delivery; d == nil || d.How != "next_run" || d.Role != "member-1" {
		t.Fatalf("delivery %+v", d)
	}
	if code, got := l.runComments(t, leader); code != 200 || len(got) != 0 {
		t.Fatalf("delivered again %d %+v", code, got)
	}
}

// A running turn and a waiting leader are given only their own study's
// comments posted since the turn started: never an older comment, another
// study's, or another workspace's.
func TestCommentsOfOtherStudiesAndEarlierAreNeverClaimed(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	l.claim(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `INSERT INTO comment (workspace_id, card_id, author_id, body, created_at)
		SELECT c.workspace_id, c.id, c.owner_id, 'Posted before the run.', r.started_at - interval '1 minute'
		FROM card c JOIN agent_run r ON r.card_id = c.id WHERE r.id = $1`, leader.id); err != nil {
		t.Fatal(err)
	}
	elsewhere := l.s.workspace(t, l.jwt, "elsewhere").ID
	for _, w := range []string{l.ws, elsewhere} {
		if _, err := l.s.Env.Owner.Exec(t.Context(), `WITH o AS (SELECT owner_id FROM card WHERE id = $1),
			c AS (INSERT INTO card (workspace_id, number, title, question, owner_id, board_column)
				SELECT $2, 900, 't', 'Another study?', owner_id, 'grounding' FROM o RETURNING id, workspace_id, owner_id)
			INSERT INTO comment (workspace_id, card_id, author_id, body) SELECT workspace_id, id, owner_id, 'For another study.' FROM c`, l.card.ID, w); err != nil {
			t.Fatal(err)
		}
	}
	if n := l.s.count(t, "SELECT count(*) FROM comment WHERE body = 'For another study.'"); n != 2 {
		t.Fatalf("%d other comments", n)
	}
	if code, got := l.runComments(t, leader); code != 200 || len(got) != 0 {
		t.Fatalf("running turn took %d %+v", code, got)
	}
	if code, out := l.result(t, leader.token, ask.Ticket); code != 200 || len(out.Comments) != 0 {
		t.Fatalf("member_result took %d %+v", code, out.Comments)
	}
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE kind = 'comment_delivery'"); n != 0 {
		t.Fatalf("%d receipts written", n)
	}
}
