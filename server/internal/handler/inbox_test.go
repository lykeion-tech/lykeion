package handler

import (
	"sort"
	"strings"
	"testing"
)

type inboxRow struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	ReadAt     *string `json:"read_at"`
	ResolvedAt *string `json:"resolved_at"`
}

func (s *server) inboxKinds(t *testing.T, tok, ws, query string) string {
	t.Helper()
	var items []inboxRow
	if code := s.call(t, "GET", "/api/decisions"+query, tok, ws, nil, &items); code != 200 {
		t.Fatalf("inbox%s: %d", query, code)
	}
	var kinds []string
	for _, it := range items {
		if (it.ResolvedAt != nil) != (query != "") {
			t.Fatalf("inbox%s holds %+v", query, it)
		}
		kinds = append(kinds, it.Kind)
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ",")
}

// inboxKindsSeeded are the Inbox kinds a study raises; seedInbox gives a
// card one item of each.
var inboxKindsSeeded = []string{"blocked", "scoring_unavailable", "done", "study_report", "no_tool"}

// seedInbox gives a card one item of every kind and returns their ids by kind.
func (s *server) seedInbox(t *testing.T, ws, cardID, owner string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, kind := range inboxKindsSeeded {
		var id string
		if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
			VALUES ($1, $2, $3, $4, 'x') RETURNING id`, ws, cardID, owner, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[kind] = id
	}
	return ids
}

func TestStoppingAStudyResolvesItsItemsAskingForAPerson(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c, other card
	c = s.idleCard(t, a, w.ID, "q")
	other = s.idleCard(t, a, w.ID, "another")
	owner := s.userID(t, a)
	s.seedInbox(t, w.ID, c.ID, owner)
	s.seedInbox(t, w.ID, other.ID, owner)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', round = 1, state = 'blocked', block_kind = 'low_score' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]string{"reason": "not worth it"}, nil); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	// The other card's items are untouched; the informational ones stay open until read.
	// Scoring retries are the server's own and never show in Decisions.
	if got := s.inboxKinds(t, a, w.ID, ""); got != "blocked,done,done,no_tool,no_tool,study_report,study_report" {
		t.Fatalf("open: %s", got)
	}
	if got := s.inboxKinds(t, a, w.ID, "?view=history"); got != "blocked" {
		t.Fatalf("archived: %s", got)
	}
	var resolved int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'inbox.resolved'`, c.ID).Scan(&resolved); err != nil || resolved != 2 {
		t.Fatalf("inbox.resolved events: %d %v", resolved, err)
	}
}

func TestReadingAnInformationalItemResolvesIt(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	ids := s.seedInbox(t, w.ID, c.ID, s.userID(t, a))
	for _, kind := range inboxKindsSeeded {
		want := 204
		if kind == "scoring_unavailable" {
			want = 404
		}
		if code := s.call(t, "POST", "/api/decisions/"+ids[kind]+"/read", a, w.ID, nil, nil); code != want {
			t.Fatalf("read %s: %d", kind, code)
		}
	}
	if got := s.inboxKinds(t, a, w.ID, ""); got != "blocked" {
		t.Fatalf("open: %s", got)
	}
	if got := s.inboxKinds(t, a, w.ID, "?view=history"); got != "done,no_tool,study_report" {
		t.Fatalf("archived: %s", got)
	}
	for _, kind := range []string{"done", "study_report", "no_tool"} {
		var resolved int
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'inbox.resolved' AND payload->>'inbox_item_id' = $2`, c.ID, ids[kind]).Scan(&resolved); err != nil || resolved != 1 {
			t.Fatalf("%s inbox.resolved events: %d %v", kind, resolved, err)
		}
	}
}

func TestReadingResolvedBlockingItemsDoesNotResolveThemAgain(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	answerer := s.signIn(t, "answerer@lab.org")
	recipient := s.signIn(t, "recipient@lab.org")
	w := s.workspace(t, answerer, "lab")
	invitation := s.invite(t, answerer, w.ID, "recipient@lab.org", "member")
	if code := s.call(t, "POST", "/api/invitations/"+invitation+"/accept", recipient, "", nil, nil); code != 200 {
		t.Fatalf("accept invitation: %d", code)
	}
	c := s.idleCard(t, answerer, w.ID, "q")
	answererID := s.userID(t, answerer)
	recipientID := s.userID(t, recipient)
	ids := s.seedInbox(t, w.ID, c.ID, recipientID)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', round = 1, state = 'blocked', block_kind = 'low_score' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/stop", answerer, w.ID, map[string]string{"reason": "enough"}, nil); code != 200 {
		t.Fatalf("stop: %d", code)
	}
	var before []inboxRow
	if code := s.call(t, "GET", "/api/decisions?view=history", recipient, w.ID, nil, &before); code != 200 || len(before) != 1 {
		t.Fatalf("archive after stop: %d %+v", code, before)
	}
	for _, item := range before {
		if item.ResolvedAt == nil || item.ReadAt != nil {
			t.Fatalf("resolved item should remain unread: %+v", item)
		}
		if code := s.call(t, "POST", "/api/decisions/"+ids[item.Kind]+"/read", recipient, w.ID, nil, nil); code != 204 {
			t.Fatalf("read %s: %d", item.Kind, code)
		}
		var total, byAnswerer, byRecipient int
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*),
			count(*) FILTER (WHERE actor_type = 'user' AND actor_id = $3),
			count(*) FILTER (WHERE actor_type = 'user' AND actor_id = $4)
			FROM event WHERE card_id = $1 AND type = 'inbox.resolved' AND payload->>'inbox_item_id' = $2`,
			c.ID, item.ID, answererID, recipientID).Scan(&total, &byAnswerer, &byRecipient); err != nil || total != 1 || byAnswerer != 1 || byRecipient != 0 {
			t.Fatalf("%s resolution events: total=%d answerer=%d recipient=%d err=%v", item.Kind, total, byAnswerer, byRecipient, err)
		}
		var reads int
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'inbox.read'
			AND payload->>'inbox_item_id' = $2 AND actor_type = 'user' AND actor_id = $3`, w.ID, item.ID, recipientID).Scan(&reads); err != nil || reads != 1 {
			t.Fatalf("%s read events: %d %v", item.Kind, reads, err)
		}
	}
	var after []inboxRow
	if code := s.call(t, "GET", "/api/decisions?view=history", recipient, w.ID, nil, &after); code != 200 || len(after) != len(before) {
		t.Fatalf("archive after read: %d %+v", code, after)
	}
	for i, item := range after {
		if item.ID != before[i].ID || item.ReadAt == nil || item.ResolvedAt == nil || *item.ResolvedAt != *before[i].ResolvedAt {
			t.Fatalf("read changed resolution: before=%+v after=%+v", before[i], item)
		}
	}
}
