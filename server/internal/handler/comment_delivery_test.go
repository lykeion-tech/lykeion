package handler

import (
	"fmt"
	"github.com/google/uuid"
	"io/fs"
	"lykeion/server/migrations"
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

func TestCodexReplyIsIncludedAndAcknowledgedInNextAgentRun(t *testing.T) {
	l, leader := groundingLab(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness = 'codex' WHERE id = $1", leader.id); err != nil {
		t.Fatal(err)
	}
	var comment struct{ ID string }
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws,
		map[string]any{"body": "Check adult sleep quality.", "target_run_id": leader.id}, &comment); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	member := l.claim(t)
	if member.id != ask.Ticket || !strings.Contains(member.prompt, "Check adult sleep quality.") {
		t.Fatalf("next run did not receive comment: %s", member.prompt)
	}
	if n := l.receipts(t, comment.ID); n != 0 {
		t.Fatalf("claim wrote %d premature receipts", n)
	}
	l.ackComments(t, member, comment.ID)
	if d := l.listComments(t)[comment.ID].Delivery; d == nil || d.How != "next_run" || d.Role != "member-1" {
		t.Fatalf("delivery %+v", d)
	}
	if n := l.receipts(t, comment.ID); n != 1 {
		t.Fatalf("%d receipts", n)
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
	if n := l.receipts(t, id); n != 0 {
		t.Fatalf("claim wrote %d premature receipts", n)
	}
	l.ackComments(t, m, id)
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

func (l *teamLab) ackComments(t *testing.T, run teamRun, ids ...string) {
	t.Helper()
	body := map[string]any{}
	for k, v := range run.ref {
		body[k] = v
	}
	body["comment_ids"] = ids
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/comments/ack", l.personal, "", body, nil); code != 204 {
		t.Fatalf("ack: %d", code)
	}
}

func TestFrozenCommentReservationAcknowledgement(t *testing.T) {
	l, leader := groundingLab(t)
	before := l.comment(t, "Before frozen prompt.")
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	if n := l.receipts(t, before); n != 0 {
		t.Fatalf("enqueue wrote %d receipts", n)
	}
	after := l.comment(t, "After frozen prompt.")
	run := l.claim(t)
	if run.id != ask.Ticket || !strings.Contains(run.prompt, "Before frozen prompt.") || strings.Contains(run.prompt, "After frozen prompt.") {
		t.Fatalf("incorrect snapshot: %s", run.prompt)
	}
	post := func(ref map[string]any, ids []string) int {
		body := map[string]any{}
		for k, v := range ref {
			body[k] = v
		}
		body["comment_ids"] = ids
		return l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/comments/ack", l.personal, "", body, nil)
	}
	for _, field := range []string{"workspace_id", "runtime_id", "attempt"} {
		bad := map[string]any{}
		for k, v := range run.ref {
			bad[k] = v
		}
		if field == "attempt" {
			bad[field] = 99
		} else {
			bad[field] = "00000000-0000-4000-8000-000000000001"
		}
		if code := post(bad, []string{before}); code < 400 {
			t.Fatalf("accepted wrong %s: %d", field, code)
		}
	}
	if code := post(run.ref, []string{after}); code != 409 {
		t.Fatalf("ack of unreserved message: %d", code)
	}
	if n := l.receipts(t, before); n != 0 {
		t.Fatalf("claim wrote %d receipts", n)
	}
	l.ackComments(t, run, before, before)
	l.ackComments(t, run, before)
	if n := l.receipts(t, before); n != 1 {
		t.Fatalf("duplicate receipts: %d", n)
	}
	if n := l.receipts(t, after); n != 0 {
		t.Fatalf("late message receipted: %d", n)
	}
}

func TestReservedCommentCancellationAndRetry(t *testing.T) {
	l, leader := groundingLab(t)
	keep := l.comment(t, "Keep for retry.")
	cancelled := l.comment(t, "Remove before launch.")
	_, _ = l.ask(t, leader.token, "member-1", "Find papers.")
	run := l.claim(t)
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+cancelled, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	var launch struct {
		Prompt     string
		CommentIDs []string `json:"comment_ids"`
	}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/comments/launch", l.personal, "", run.ref, &launch); code != 200 {
		t.Fatalf("launch check: %d", code)
	}
	if strings.Contains(launch.Prompt, "Remove before launch.") || !strings.Contains(launch.Prompt, "Keep for retry.") || len(launch.CommentIDs) != 1 || launch.CommentIDs[0] != keep {
		t.Fatalf("launch: %+v", launch)
	}
	l.fail(t, run, "provider_network")
	if n := l.receipts(t, keep); n != 0 {
		t.Fatalf("failed launch receipt: %d", n)
	}
	// Advance the existing infra retry backoff without changing recovery policy.
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET retry_after = NULL WHERE id = $1", run.id); err != nil {
		t.Fatal(err)
	}
	retry := l.claim(t)
	if retry.id != run.id || retry.ref["attempt"] == run.ref["attempt"] {
		t.Fatalf("not retried: %+v", retry)
	}
	old := map[string]any{}
	for k, v := range run.ref {
		old[k] = v
	}
	old["comment_ids"] = []string{keep}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/comments/ack", l.personal, "", old, nil); code != 409 {
		t.Fatalf("stale ack: %d", code)
	}
	l.ackComments(t, retry, keep, cancelled)
	if n := l.receipts(t, keep); n != 1 {
		t.Fatalf("retry receipts: %d", n)
	}
	if n := l.receipts(t, cancelled); n != 0 {
		t.Fatalf("cancelled receipt: %d", n)
	}
}

func TestReservedCommentRecoveredAfterTerminalRun(t *testing.T) {
	l, leader := groundingLab(t)
	id := l.comment(t, "Pending across failed work.")
	_, _ = l.ask(t, leader.token, "member-1", "First try.")
	run := l.claim(t)
	l.fail(t, run, "agent_error")
	if n := l.receipts(t, id); n != 0 {
		t.Fatalf("failed run receipt: %d", n)
	}
	_, ask := l.ask(t, leader.token, "member-1", "Try a different approach.")
	next := l.claim(t)
	if next.id != ask.Ticket || next.id == run.id || !strings.Contains(next.prompt, "Pending across failed work.") {
		t.Fatalf("wrong recovery run: %+v", next)
	}
	l.ackComments(t, next, id)
	if n := l.receipts(t, id); n != 1 {
		t.Fatalf("recovered receipt count: %d", n)
	}
	// Settlement is a fence even for a duplicate acknowledgement.
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", next.id); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{}
	for k, v := range next.ref {
		body[k] = v
	}
	body["comment_ids"] = []string{id}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+next.id+"/comments/ack", l.personal, "", body, nil); code != 409 {
		t.Fatalf("settled ack: %d", code)
	}
}

func TestLaunchFiltersAllSnapshotCommentsByIdentity(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprint("delivered=", delivered), func(t *testing.T) {
			l, leader := groundingLab(t)
			keep := l.comment(t, "Please - use controls")
			cancelled := l.comment(t, "use controls")
			if delivered {
				if code, cs := l.runComments(t, leader); code != 200 || len(cs) != 2 {
					t.Fatalf("deliver %d %+v", code, cs)
				}
			}
			_, _ = l.ask(t, leader.token, "member-1", "Find papers.")
			run := l.claim(t)
			if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+cancelled, l.jwt, l.ws, nil, nil); code != 204 {
				t.Fatalf("cancel %d", code)
			}
			var launch struct {
				Prompt     string
				CommentIDs []string `json:"comment_ids"`
			}
			if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/comments/launch", l.personal, "", run.ref, &launch); code != 200 {
				t.Fatalf("preflight %d", code)
			}
			if !strings.Contains(launch.Prompt, "- Please - use controls\n") || strings.Contains(launch.Prompt, "\n- use controls\n") {
				t.Fatalf("bad filtered prompt: %s", launch.Prompt)
			}
			if !delivered && (len(launch.CommentIDs) != 1 || launch.CommentIDs[0] != keep) {
				t.Fatalf("ids %+v", launch.CommentIDs)
			}
		})
	}
}

func TestMachineRecoveryCopiesCommentSnapshotAndReservations(t *testing.T) {
	l, leader := groundingLab(t)
	keep := l.comment(t, "Pending before machine switch.")
	cancelled := l.comment(t, "Cancel before machine switch.")
	_, _ = l.ask(t, leader.token, "member-1", "Find papers.")
	original := l.claim(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled',cancel_reason='runtime_access_revoked' WHERE id=$1`, original.id); err != nil {
		t.Fatal(err)
	}
	l.comment(t, "Posted after original frozen prompt.")
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+cancelled, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	if code := l.s.call(t, "PATCH", "/api/cards/"+l.card.ID+"/machine", l.jwt, l.ws, map[string]any{"machine": "mine"}, nil); code != 200 {
		t.Fatalf("machine switch: %d", code)
	}
	replacement := l.claim(t)
	if replacement.id == original.id || !strings.Contains(replacement.prompt, "Pending before machine switch.") || strings.Contains(replacement.prompt, "Cancel before machine switch.") || strings.Contains(replacement.prompt, "Posted after original frozen prompt.") {
		t.Fatalf("recovery snapshot: %s", replacement.prompt)
	}
	l.ackComments(t, replacement, keep)
	if l.receipts(t, keep) != 1 || l.receipts(t, cancelled) != 0 {
		t.Fatal("incorrect recovery receipts")
	}
}

func TestLaunchFiltersCommentReservedByAnotherRun(t *testing.T) {
	l, leader := groundingLab(t)
	id := l.comment(t, "Shared frozen context.")
	// Model a pending input owned by the active leader's frozen prompt. Only
	// one member may work per column; no second member is needed for this case.
	if _, err := l.s.Env.Owner.Exec(t.Context(), `INSERT INTO comment_reservation(workspace_id,comment_id,agent_run_id,attempt) VALUES($1,$2,$3,$4)`, l.ws, id, leader.id, leader.ref["attempt"]); err != nil {
		t.Fatal(err)
	}
	if code, out := l.ask(t, leader.token, "member-1", "Find papers."); code != 201 {
		t.Fatalf("ask: %d %+v", code, out)
	}
	member := l.claim(t)
	if !strings.Contains(member.prompt, "Shared frozen context.") {
		t.Fatal("missing shared context")
	}
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+id, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	var launch struct {
		Prompt     string
		CommentIDs []string `json:"comment_ids"`
	}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+member.id+"/comments/launch", l.personal, "", member.ref, &launch); code != 200 {
		t.Fatalf("preflight: %d", code)
	}
	if strings.Contains(launch.Prompt, "Shared frozen context.") || len(launch.CommentIDs) != 0 {
		t.Fatalf("cancelled context or foreign ownership survived: %+v", launch)
	}
}

// Seed historical inputs without firing OnComment: these tests rewind schema
// state and must not launch asynchronous scheduling against that temporary DDL.
func (l *teamLab) legacyPromptComment(t *testing.T, body string) string {
	t.Helper()
	var id string
	if err := l.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO comment(workspace_id,card_id,author_id,body) SELECT workspace_id,id,owner_id,$2 FROM card WHERE id=$1 RETURNING id`, l.card.ID, body).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Recreate the pre-0062 enqueue receipt state, then apply the actual migration.
// Historical inputs are seeded without asynchronous scheduling above; bound
// DDL lock waits as a guard against accidentally introducing concurrent work.
func (l *teamLab) migrateLegacyCommentPrompt(t *testing.T, run string, ids ...string) {
	t.Helper()
	down, err := fs.ReadFile(migrations.FS, "0062_comment_reservations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := fs.ReadFile(migrations.FS, "0062_comment_reservations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := l.s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "SET LOCAL lock_timeout = '5s'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), string(down)); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err = tx.Exec(t.Context(), `INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,body,agent_run_id) SELECT workspace_id,card_id,board_column,round,'comment_delivery',role,jsonb_build_object('comment_id',$2::text,'how','next_run'),id FROM agent_run WHERE id=$1`, run, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMigratedCommentPromptFiltersCancellationBeforeLaunch(t *testing.T) {
	l, leader := groundingLab(t)
	keep := l.legacyPromptComment(t, "Please - use controls")
	cancelled := l.legacyPromptComment(t, "use controls")
	code, queued := l.ask(t, leader.token, "member-1", "Find papers.")
	if code != 201 {
		t.Fatalf("ask %d", code)
	}
	l.migrateLegacyCommentPrompt(t, queued.Ticket, keep, cancelled)
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+cancelled, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatalf("cancel %d", code)
	}
	run := l.claim(t)
	var launch struct {
		Prompt     string
		CommentIDs []string `json:"comment_ids"`
	}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.id+"/comments/launch", l.personal, "", run.ref, &launch); code != 200 {
		t.Fatalf("launch %d", code)
	}
	if !strings.Contains(launch.Prompt, "\n- Please - use controls\n") || strings.Contains(launch.Prompt, "\n- use controls\n") {
		t.Fatalf("migrated cancellation leaked: %s", launch.Prompt)
	}
	if len(launch.CommentIDs) != 1 || launch.CommentIDs[0] != keep {
		t.Fatalf("launch ids %v", launch.CommentIDs)
	}
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='comment_delivery' AND COALESCE(body->>'historical_unconfirmed','false')<>'true'`, run.id); n != 0 {
		t.Fatalf("fabricated receipts: %d", n)
	}
	l.ackComments(t, run, keep)
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='comment_delivery' AND COALESCE(body->>'historical_unconfirmed','false')<>'true'`, run.id); n != 1 {
		t.Fatalf("ack receipts: %d", n)
	}
}

func TestAmbiguousMigratedCommentDoesNotBlockOtherStudies(t *testing.T) {
	l, leader := groundingLab(t)
	first := l.legacyPromptComment(t, "Identical text")
	second := l.legacyPromptComment(t, "Identical text")
	code, queued := l.ask(t, leader.token, "member-1", "Find papers.")
	if code != 201 {
		t.Fatal(code)
	}
	l.migrateLegacyCommentPrompt(t, queued.Ticket, first, second)
	if code := l.s.call(t, "DELETE", "/api/cards/"+l.card.ID+"/comments/"+second, l.jwt, l.ws, nil, nil); code != 204 {
		t.Fatal(code)
	}

	// Put valid work on either side of the poisoned queue entry. Both must
	// survive the same claim transaction, including the earlier claim.
	var valid []string
	for i, offset := range []string{"-1 second", "1 second"} {
		var cardID, runID string
		if err := l.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO card(workspace_id,number,title,question,owner_id,board_column) SELECT workspace_id,$2,'Other study','Valid work',owner_id,board_column FROM card WHERE id=$1 RETURNING id`, l.card.ID, 900+i).Scan(&cardID); err != nil {
			t.Fatal(err)
		}
		if err := l.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run(workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,created_at) SELECT workspace_id,$2,responsible_user_id,board_column,round,'leader',harness,'Unrelated valid work.',created_at+$3::interval FROM agent_run WHERE id=$1 RETURNING id`, queued.Ticket, cardID, offset).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		valid = append(valid, runID)
	}
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET max_concurrent=4 WHERE id=$1", l.rts); err != nil {
		t.Fatal(err)
	}
	ref := map[string]any{"workspace_id": l.ws, "runtime_id": l.rts}
	var claims []struct {
		AgentRun struct{ ID string } `json:"agent_run"`
	}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/claim", l.personal, "", map[string]any{"runtimes": []any{ref}, "slots": 2}, &claims); code != 200 {
		t.Fatalf("one malformed run blocked batch: %d", code)
	}
	if len(claims) != 2 || claims[0].AgentRun.ID != valid[0] || claims[1].AgentRun.ID != valid[1] {
		t.Fatalf("valid claims lost: %+v", claims)
	}
	if n := l.s.count(t, `SELECT count(*) FROM agent_run WHERE id=$1 AND status='failed' AND error LIKE '%legacy comment identity%'`, queued.Ticket); n != 1 {
		t.Fatal("invalid legacy run not visibly failed")
	}
	if n := l.s.count(t, `SELECT count(*) FROM comment_reservation WHERE agent_run_id=$1 AND acknowledged_at IS NULL`, queued.Ticket); n != 2 {
		t.Fatalf("pending messages lost: %d", n)
	}
	if n := l.s.count(t, `SELECT count(*) FROM agent_run_token WHERE agent_run_id=$1`, queued.Ticket); n != 0 {
		t.Fatalf("tokens issued for refused run: %d", n)
	}

	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='comment_delivery' AND COALESCE(body->>'historical_unconfirmed','false')<>'true'`, queued.Ticket); n != 0 {
		t.Fatalf("fabricated receipts: %d", n)
	}
}

func TestComposerFollowUpClaimsAndHandsIn(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprint(single), func(t *testing.T) {
			l, old := groundingLab(t)
			role := "leader"
			if single {
				role = "agent"
				if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE card SET assignee_type='agent',assignee_id=(SELECT agent_id FROM agent_run WHERE id=$2) WHERE id=$1`, l.card.ID, old.id); err != nil {
					t.Fatal(err)
				}
				if _, err := l.s.Env.Owner.Exec(t.Context(), `DELETE FROM card_team WHERE card_id=$1`, l.card.ID); err != nil {
					t.Fatal(err)
				}
			}
			// A durable settled column awaiting further input, with no active work.
			if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='completed' WHERE id=$1`, old.id); err != nil {
				t.Fatal(err)
			}
			reply := map[string]any{"body": "Run-card discussion only.", "target_run_id": old.id}
			if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, reply, nil); code != 201 {
				t.Fatalf("reply %d", code)
			}
			if l.queued(t) != 0 {
				t.Fatal("run reply woke study")
			}
			if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET status='offline' WHERE id=$1", l.rts); err != nil {
				t.Fatal(err)
			}
			id := l.comment(t, "Check the independent control cohort exactly.")
			if err := l.s.H.Engine.Advance(t.Context(), uuid.MustParse(l.ws), uuid.MustParse(l.card.ID)); err != nil {
				t.Fatal(err)
			}
			if l.queued(t) != 1 || l.receipts(t, id) != 0 {
				t.Fatal("offline message lost or falsely delivered")
			}
			if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET status='online' WHERE id=$1", l.rts); err != nil {
				t.Fatal(err)
			}
			next := l.claim(t)
			if next.role != role || !strings.Contains(next.prompt, "Check the independent control cohort exactly.") {
				t.Fatalf("wrong claimed input: %+v", next)
			}
			if l.receipts(t, id) != 0 {
				t.Fatal("premature delivered status")
			}
			var launch struct {
				Prompt     string
				CommentIDs []string `json:"comment_ids"`
			}
			if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+next.id+"/comments/launch", l.personal, "", next.ref, &launch); code != 200 || !strings.Contains(launch.Prompt, "Check the independent control cohort exactly.") {
				t.Fatalf("launch: %d %+v", code, launch)
			}
			l.ackComments(t, next, launch.CommentIDs...)
			hand := grounding()
			hand["answer"] = "Checked the requested independent cohort."
			hand["no_qualifying"] = map[string]any{"reason": "No independent cohort met the inclusion criteria."}
			if out := l.handIn(t, next, hand); !out.Accepted {
				t.Fatalf("hand-in rejected: %+v", out)
			}
			l.complete(t, next)
			if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='summary' AND body->>'text'='Checked the requested independent cohort.'`, next.id); n != 1 {
				t.Fatalf("missing new response: %d", n)
			}
			if l.receipts(t, id) != 1 {
				t.Fatal("missing actual acknowledgement")
			}
			if !single {
				var runtimes []struct{ ID string }
				if code := l.s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": l.ws, "daemon_id": "audit-daemon", "harnesses": []string{"codex"}}, &runtimes); code != 200 || len(runtimes) != 1 {
					t.Fatalf("auditor runtime: %d %+v", code, runtimes)
				}
				l.rts = runtimes[0].ID
				audit := l.claim(t)
				if audit.role != "auditor" || !strings.Contains(audit.prompt, "Checked the requested independent cohort.") {
					t.Fatalf("normal gate bypassed: %+v", audit)
				}
			}
		})
	}
}
