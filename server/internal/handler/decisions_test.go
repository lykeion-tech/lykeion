package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

type decisionRow struct {
	QuestionSet  json.RawMessage `json:"question_set"`
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	Text         string          `json:"text"`
	Kept         bool            `json:"kept"`
	CanAnswer    bool            `json:"can_answer"`
	DecideBy     string          `json:"decide_by"`
	SnoozedUntil *string         `json:"snoozed_until"`
	ArchivedAt   *string         `json:"archived_at"`
}

const stopOptions = `[{"id":"close","label":"Close","consequence":"c"},{"id":"park","label":"Park","consequence":"p"}]`

// seedDecision inserts an open item with options for a card and returns its id.
func (s *server) seedDecision(t *testing.T, ws, cardID, user, text string) string {
	t.Helper()
	var id string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, options, default_option)
		VALUES ($1, $2, $3, 'needs_decision', $4, $5::jsonb, 'park') RETURNING id`, ws, cardID, user, text, stopOptions).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (s *server) decisions(t *testing.T, tok, ws, query string) []decisionRow {
	t.Helper()
	var rows []decisionRow
	if code := s.call(t, "GET", "/api/decisions"+query, tok, ws, nil, &rows); code != 200 {
		t.Fatalf("decisions%s: %d", query, code)
	}
	return rows
}

func (s *server) decisionCount(t *testing.T, tok, ws string) int {
	t.Helper()
	var out struct{ Open int }
	if code := s.call(t, "GET", "/api/decisions/count", tok, ws, nil, &out); code != 200 {
		t.Fatalf("count: %d", code)
	}
	return out.Open
}

func TestDecisionsVisibleToStudyReaders(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	c := s.idleCard(t, a, w.ID, "q")
	s.seedDecision(t, w.ID, c.ID, s.userID(t, a), "stopped")
	// A machine item of A is A's alone.
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO inbox_item (workspace_id, user_id, kind, text) VALUES ($1, $2, 'machine_offline', 'laptop')`, w.ID, s.userID(t, a)); err != nil {
		t.Fatal(err)
	}
	if got := s.decisions(t, b, w.ID, ""); len(got) != 1 || got[0].Text != "stopped" || got[0].CanAnswer {
		t.Fatalf("B sees %+v", got)
	}
	if got := s.decisions(t, a, w.ID, ""); len(got) != 2 {
		t.Fatalf("A sees %+v", got)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE inbox_item SET status = 'decided', resolved_at = now() WHERE kind = 'needs_decision'`); err != nil {
		t.Fatal(err)
	}
	if got := s.decisions(t, b, w.ID, "?view=history"); len(got) != 1 || got[0].Text != "stopped" {
		t.Fatalf("B history %+v", got)
	}
	// A member of only another workspace sees nothing of A's, by list or id.
	outsider := s.signIn(t, "out@lab.org")
	other := s.workspace(t, outsider, "other")
	id := s.seedDecision(t, w.ID, c.ID, s.userID(t, a), "open one")
	for _, q := range []string{"", "?view=history"} {
		if got := s.decisions(t, outsider, other.ID, q); len(got) != 0 {
			t.Fatalf("another workspace shows %+v", got)
		}
	}
	// Naming A's workspace is refused outright.
	for _, path := range []string{"/api/decisions", "/api/decisions?view=history", "/api/decisions/count"} {
		var body map[string]any
		if code := s.call(t, "GET", path, outsider, w.ID, nil, &body); code != 403 && code != 404 {
			t.Fatalf("outsider GET %s with A's workspace: %d %+v", path, code, body)
		}
	}
	if code := s.call(t, "PUT", "/api/decisions/"+id+"/triage", outsider, other.ID, map[string]any{"decide_by": "today"}, nil); code != 404 {
		t.Fatalf("triage across workspaces: %d", code)
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/read", outsider, other.ID, nil, nil); code != 404 {
		t.Fatalf("read across workspaces: %d", code)
	}
}

func TestTriageOrdersAndSnoozes(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	user := s.userID(t, a)
	whenever := s.seedDecision(t, w.ID, c.ID, user, "whenever")
	today := s.seedDecision(t, w.ID, c.ID, user, "today")
	snoozed := s.seedDecision(t, w.ID, c.ID, user, "snoozed")
	triage := func(id string, body map[string]any) {
		t.Helper()
		if code := s.call(t, "PUT", "/api/decisions/"+id+"/triage", a, w.ID, body, nil); code != 204 {
			t.Fatalf("triage: %d", code)
		}
	}
	triage(whenever, map[string]any{"decide_by": "whenever"})
	triage(today, map[string]any{"decide_by": "today"})
	triage(snoozed, map[string]any{"decide_by": "today", "snoozed_until": time.Now().Add(48 * time.Hour).Format(time.RFC3339)})
	got := s.decisions(t, a, w.ID, "")
	if len(got) != 3 || got[0].Text != "today" || got[1].Text != "whenever" || got[2].Text != "snoozed" || got[2].SnoozedUntil == nil {
		t.Fatalf("order: %+v", got)
	}
	if n := s.decisionCount(t, a, w.ID); n != 2 {
		t.Fatalf("count with one snoozed: %d", n)
	}
	if code := s.call(t, "PUT", "/api/decisions/"+today+"/triage", a, w.ID, map[string]any{"decide_by": "date"}, nil); code != 400 {
		t.Fatalf("date without a date: %d", code)
	}
	if code := s.call(t, "PUT", "/api/decisions/"+today+"/triage", a, w.ID, map[string]any{"decide_by": "soon"}, nil); code != 400 {
		t.Fatalf("unknown decide_by: %d", code)
	}
}

func TestKeepArchiveRevive(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	c := s.idleCard(t, a, w.ID, "q")
	id := s.seedDecision(t, w.ID, c.ID, s.userID(t, a), "x")
	post := func(tok, action string) int {
		return s.call(t, "POST", "/api/decisions/"+id+"/"+action, tok, w.ID, nil, nil)
	}
	if code := post(b, "archive"); code != 403 {
		t.Fatalf("a plain member archiving another's item: %d", code)
	}
	if code := post(a, "keep"); code != 204 {
		t.Fatalf("keep: %d", code)
	}
	if got := s.decisions(t, a, w.ID, ""); len(got) != 1 || !got[0].Kept {
		t.Fatalf("kept: %+v", got)
	}
	// An open decision is answered, not archived.
	if code := post(a, "archive"); code != 409 {
		t.Fatalf("archiving an open decision: %d", code)
	}
	var info string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) VALUES ($1, $2, $3, 'no_tool', 'x')
		RETURNING id`, w.ID, c.ID, s.userID(t, a)).Scan(&info); err != nil {
		t.Fatal(err)
	}
	id = info
	if code := post(a, "archive"); code != 204 {
		t.Fatalf("archive: %d", code)
	}
	if got := s.decisions(t, a, w.ID, ""); len(got) != 1 || got[0].Kind != "needs_decision" {
		t.Fatalf("archived still open: %+v", got)
	}
	if got := s.decisions(t, a, w.ID, "?view=history"); len(got) != 1 || got[0].ArchivedAt == nil || got[0].CanAnswer {
		t.Fatalf("history: %+v", got)
	}
	if code := post(a, "revive"); code != 204 {
		t.Fatalf("revive: %d", code)
	}
	if got := s.decisions(t, a, w.ID, ""); len(got) != 2 {
		t.Fatalf("revived: %+v", got)
	}
}

// A member opening the owner's unread item is not its addressee: the read
// is accepted and changes nothing.
func TestReadingAnothersItemChangesNothing(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	c := s.idleCard(t, a, w.ID, "q")
	id := s.seedDecision(t, w.ID, c.ID, s.userID(t, a), "stopped")
	if code := s.call(t, "POST", "/api/decisions/"+id+"/read", b, w.ID, nil, nil); code != 204 {
		t.Fatalf("member reading the owner's item: %d", code)
	}
	var unread bool
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT read_at IS NULL FROM inbox_item WHERE id = $1`, id).Scan(&unread); err != nil || !unread {
		t.Fatalf("read_at changed: unread %v, %v", unread, err)
	}
	var events int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE type = 'inbox.read'`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("inbox.read events: %d, %v", events, err)
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("owner reading: %d", code)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT read_at IS NULL FROM inbox_item WHERE id = $1`, id).Scan(&unread); err != nil || unread {
		t.Fatalf("owner's read not recorded: %v", err)
	}
}

// due_soon counts the open answerable decisions whose reminder went out.
func TestCountsDecisionsDueSoon(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	user := s.userID(t, a)
	s.seedDecision(t, w.ID, c.ID, user, "later")
	soon := s.seedDecision(t, w.ID, c.ID, user, "soon")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE inbox_item SET reminded_at = now() WHERE id = $1`, soon); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Open    int `json:"open"`
		DueSoon int `json:"due_soon"`
	}
	if code := s.call(t, "GET", "/api/decisions/count", a, w.ID, nil, &out); code != 200 || out.Open != 2 || out.DueSoon != 1 {
		t.Fatalf("count: %d %+v", code, out)
	}
	var rows []struct {
		Text       string  `json:"text"`
		RemindedAt *string `json:"reminded_at"`
	}
	s.call(t, "GET", "/api/decisions", a, w.ID, nil, &rows)
	for _, r := range rows {
		if (r.Text == "soon") != (r.RemindedAt != nil) {
			t.Fatalf("reminded_at: %+v", rows)
		}
	}
}

func TestCountCountsAnswerableOnly(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	owner := s.signIn(t, "owner@lab.org")
	w := s.workspace(t, owner, "lab")
	member := s.member(t, owner, w.ID, "member@lab.org", "member")
	admin := s.member(t, owner, w.ID, "admin@lab.org", "admin")
	c := s.idleCard(t, member, w.ID, "q")
	s.seedDecision(t, w.ID, c.ID, s.userID(t, member), "x")
	other := s.idleCard(t, owner, w.ID, "mine")
	s.seedDecision(t, w.ID, other.ID, s.userID(t, owner), "y")
	// An informational item never counts.
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) VALUES ($1, $2, $3, 'done', 'z')`, w.ID, other.ID, s.userID(t, owner)); err != nil {
		t.Fatal(err)
	}
	if n := s.decisionCount(t, member, w.ID); n != 1 {
		t.Fatalf("member (owns one study): %d", n)
	}
	if n := s.decisionCount(t, owner, w.ID); n != 2 {
		t.Fatalf("workspace owner: %d", n)
	}
	if n := s.decisionCount(t, admin, w.ID); n != 2 {
		t.Fatalf("admin: %d", n)
	}
	plain := s.member(t, owner, w.ID, "plain@lab.org", "member")
	if n := s.decisionCount(t, plain, w.ID); n != 0 {
		t.Fatalf("plain member: %d", n)
	}
}

func TestHistoryIsNewestFirstDespiteSnooze(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	user := s.userID(t, a)
	s.seedDecision(t, w.ID, c.ID, user, "older")
	newer := s.seedDecision(t, w.ID, c.ID, user, "newer")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE inbox_item SET status = 'decided', resolved_at = now(),
		snoozed_until = CASE WHEN id = $1 THEN now() + interval '2 days' END`, newer); err != nil {
		t.Fatal(err)
	}
	if got := s.decisions(t, a, w.ID, "?view=history"); len(got) != 2 || got[0].Text != "newer" {
		t.Fatalf("history order: %+v", got)
	}
}

// heldCard is an idle study held blocked for its owner, with its stop
// decision.
func (s *server) heldCard(t *testing.T, tok, ws string) (card, string) {
	t.Helper()
	c := s.idleCard(t, tok, ws, "q")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET state = 'blocked' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, cause, options, default_option)
		VALUES ($1, $2, $3, 'needs_decision', 'held', 'could_not_do', $4::jsonb, 'park') RETURNING id`, ws, c.ID, s.userID(t, tok),
		`[{"id":"redo","label":"R","consequence":"r"},{"id":"close","label":"C","consequence":"c"},{"id":"park","label":"P","consequence":"p"}]`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return c, id
}

func TestAdminMayAnswerMemberMayNot(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	w := s.workspace(t, owner, "lab")
	member := s.member(t, owner, w.ID, "member@lab.org", "member")
	admin := s.member(t, owner, w.ID, "admin@lab.org", "admin")
	c, id := s.heldCard(t, member, w.ID)
	plain := s.member(t, owner, w.ID, "plain@lab.org", "member")
	if code := s.call(t, "POST", "/api/decisions/"+id+"/decide", plain, w.ID, map[string]any{"option": "park"}, nil); code != 403 {
		t.Fatalf("a plain member answered: %d", code)
	}
	var out struct {
		decisionRow
		Status       string `json:"status"`
		ChosenOption string `json:"chosen_option"`
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/decide", admin, w.ID, map[string]any{"option": "park"}, &out); code != 200 || out.Status != "decided" || out.ChosenOption != "park" || out.CanAnswer {
		t.Fatalf("admin: %d %+v", code, out)
	}
	var state, kind string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT state, block_kind FROM card WHERE id = $1`, c.ID).Scan(&state, &kind); err != nil || state != "blocked" || kind != "parked" {
		t.Fatalf("card %s %s %v", state, kind, err)
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/decide", member, w.ID, map[string]any{"option": "close"}, nil); code != 409 {
		t.Fatalf("answering a decided item: %d", code)
	}
}

func TestDecideRefusesBadAnswers(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, id := s.heldCard(t, a, w.ID)
	for _, body := range []map[string]any{{"option": "try_again"}, {"option": "redo"}, {"option": "redo", "guidance": " "}} {
		if code := s.call(t, "POST", "/api/decisions/"+id+"/decide", a, w.ID, body, nil); code != 400 {
			t.Fatalf("%v: %d", body, code)
		}
	}
	if code := s.call(t, "POST", "/api/decisions/"+uuid.NewString()+"/decide", a, w.ID, map[string]any{"option": "park"}, nil); code != 404 {
		t.Fatalf("an unknown item: %d", code)
	}
}

func TestStoppingCancelsOpenDecisions(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c, id := s.heldCard(t, a, w.ID)
	other := s.idleCard(t, a, w.ID, "other")
	untouched := s.seedDecision(t, w.ID, other.ID, s.userID(t, a), "other")
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]any{"reason": "enough"}, nil); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	status := func(id string) string {
		var st string
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM inbox_item WHERE id = $1`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if got := status(id); got != "cancelled" {
		t.Fatalf("the stopped study's decision: %s", got)
	}
	if got := status(untouched); got != "open" {
		t.Fatalf("another study's decision: %s", got)
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/decide", a, w.ID, map[string]any{"option": "park"}, nil); code != 409 {
		t.Fatalf("answering a cancelled decision: %d", code)
	}
}

// Parking stops the study: the runs its machines kept failing are cancelled,
// so none stays queued for ever.
func TestDecideParkCancelsHeldRuns(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "Why?")
	var run, item string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, infra_failures, failure_reason, retry_after)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p', 'queued', 5, 5, 'provider_network', '9999-12-31') RETURNING id::text`, w.ID, c.ID, c.OwnerID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, cause, options, default_option)
		VALUES ($1, $2, $3, 'needs_decision', 't', 'machine_failures', '[{"id":"try_again","label":"T","consequence":"t"},{"id":"park","label":"P","consequence":"p"}]', 'park') RETURNING id::text`,
		w.ID, c.ID, c.OwnerID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/decisions/"+item+"/decide", a, w.ID, map[string]any{"option": "park"}, nil); code != 200 {
		t.Fatalf("park: %d", code)
	}
	var queued int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM agent_run WHERE card_id = $1 AND status IN ('queued', 'dispatched', 'running')`, c.ID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	var state, kind string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT state, block_kind FROM card WHERE id = $1`, c.ID).Scan(&state, &kind); err != nil {
		t.Fatal(err)
	}
	if queued != 0 || state != "blocked" || kind != "parked" {
		t.Fatalf("after park: %d open runs, card %s %s", queued, state, kind)
	}
}

// A pass with open doubts only informs: reading it resolves and dismisses it.
func TestReadingOpenDoubtsResolvesIt(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	var id string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) VALUES ($1, $2, $3, 'open_doubts', 'x')
		RETURNING id`, w.ID, c.ID, s.userID(t, a)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("read: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE id = $1 AND status = 'dismissed' AND resolved_at IS NOT NULL`, id); n != 1 {
		t.Fatal("open doubts stayed open after reading")
	}
}

func TestTextOnlyDecisionVisibilityAndArchive(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	owner := s.signIn(t, "owner-set@lab.org")
	w := s.workspace(t, owner, "question-set")
	member := s.member(t, owner, w.ID, "member-set@lab.org", "member")
	card := s.idleCard(t, owner, w.ID, "question")
	var id string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind)
	 VALUES ($1,$2,$3,'agent_decision','question','[]','{"questions":[{"id":"q","prompt":"Which population?","help_text":null,"required":true,"mode":"text","options":[],"allow_other":false}]}','agent','scope') RETURNING id`, w.ID, card.ID, s.userID(t, owner)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if got := s.decisions(t, owner, w.ID, ""); len(got) != 1 || !got[0].CanAnswer || len(got[0].QuestionSet) == 0 || got[0].QuestionSet[0] != '{' {
		t.Fatalf("owner list: %+v", got)
	}
	if got := s.decisions(t, member, w.ID, ""); len(got) != 1 || got[0].CanAnswer {
		t.Fatalf("member list: %+v", got)
	}
	if got := s.decisionCount(t, owner, w.ID); got != 1 {
		t.Fatalf("owner count %d", got)
	}
	if got := s.decisionCount(t, member, w.ID); got != 0 {
		t.Fatalf("member count %d", got)
	}
	if code := s.call(t, "POST", "/api/decisions/"+id+"/archive", owner, w.ID, nil, nil); code != 409 {
		t.Fatalf("archive open decision: %d", code)
	}
	other := s.signIn(t, "outsider-set@lab.org")
	otherWS := s.workspace(t, other, "other-question-set")
	if got := s.decisions(t, other, otherWS.ID, ""); len(got) != 0 {
		t.Fatalf("cross workspace: %+v", got)
	}
}
