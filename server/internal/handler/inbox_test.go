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
	if code := s.call(t, "GET", "/api/inbox"+query, tok, ws, nil, &items); code != 200 {
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

// seedInbox gives a blocked card one item of every kind and returns their ids by kind.
func (s *server) seedInbox(t *testing.T, ws, cardID, owner string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, kind := range []string{"escalated", "out_of_budget", "released", "publish_request"} {
		var id string
		if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
			VALUES ($1, $2, $3, $4, 'x') RETURNING id`, ws, cardID, owner, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[kind] = id
	}
	return ids
}

func TestAnsweringABlockedCardResolvesItsInboxItems(t *testing.T) {
	for _, action := range []string{"resume", "stop", "budget"} {
		t.Run(action, func(t *testing.T) {
			s := newServer(t)
			s.H.Engine = nil
			a := s.signIn(t, "a@lab.org")
			w := s.workspace(t, a, "lab")
			var c, other card
			s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
			s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "another"}, &other)
			owner := s.userID(t, a)
			s.seedInbox(t, w.ID, c.ID, owner)
			s.seedInbox(t, w.ID, other.ID, owner)
			if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', round = 1, flag = 'escalated' WHERE id = $1`, c.ID); err != nil {
				t.Fatal(err)
			}
			if got := s.inboxKinds(t, a, w.ID, "?archived=1"); got != "" {
				t.Fatalf("archive before answering: %s", got)
			}
			var body any
			switch action {
			case "stop":
				body = map[string]string{"reason": "not worth it"}
			case "budget":
				body = map[string]float64{"usd": 30}
			}
			if code := s.call(t, "POST", "/api/cards/"+c.ID+"/"+action, a, w.ID, body, nil); code != 200 {
				t.Fatalf("%s: %d", action, code)
			}
			// The other card's items are untouched; released and publish_request stay open.
			if got := s.inboxKinds(t, a, w.ID, ""); got != "escalated,out_of_budget,publish_request,publish_request,released,released" {
				t.Fatalf("open: %s", got)
			}
			if got := s.inboxKinds(t, a, w.ID, "?archived=1"); got != "escalated,out_of_budget" {
				t.Fatalf("archived: %s", got)
			}
			var resolved int
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'inbox.resolved'`, c.ID).Scan(&resolved); err != nil || resolved != 2 {
				t.Fatalf("inbox.resolved events: %d %v", resolved, err)
			}
		})
	}
}

func TestReadingAReleasedItemResolvesIt(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	ids := s.seedInbox(t, w.ID, c.ID, s.userID(t, a))
	for _, kind := range []string{"released", "escalated", "publish_request"} {
		if code := s.call(t, "POST", "/api/inbox/"+ids[kind]+"/read", a, w.ID, nil, nil); code != 204 {
			t.Fatalf("read %s: %d", kind, code)
		}
	}
	if got := s.inboxKinds(t, a, w.ID, ""); got != "escalated,out_of_budget,publish_request" {
		t.Fatalf("open: %s", got)
	}
	var archived []inboxRow
	s.call(t, "GET", "/api/inbox?archived=1", a, w.ID, nil, &archived)
	if len(archived) != 1 || archived[0].ID != ids["released"] || archived[0].ReadAt == nil {
		t.Fatalf("archived: %+v", archived)
	}
	var resolved int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'inbox.resolved' AND payload->>'inbox_item_id' = $2`, c.ID, ids["released"]).Scan(&resolved); err != nil || resolved != 1 {
		t.Fatalf("inbox.resolved events: %d %v", resolved, err)
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
	var c card
	if code := s.call(t, "POST", "/api/cards", answerer, w.ID, map[string]string{"question": "q"}, &c); code != 201 {
		t.Fatalf("create card: %d", code)
	}
	answererID := s.userID(t, answerer)
	recipientID := s.userID(t, recipient)
	ids := s.seedInbox(t, w.ID, c.ID, recipientID)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', round = 1, flag = 'escalated' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/resume", answerer, w.ID, nil, nil); code != 200 {
		t.Fatalf("resume: %d", code)
	}
	var before []inboxRow
	if code := s.call(t, "GET", "/api/inbox?archived=1", recipient, w.ID, nil, &before); code != 200 || len(before) != 2 {
		t.Fatalf("archive after resume: %d %+v", code, before)
	}
	for _, item := range before {
		if item.ResolvedAt == nil || item.ReadAt != nil {
			t.Fatalf("resolved item should remain unread: %+v", item)
		}
		if code := s.call(t, "POST", "/api/inbox/"+ids[item.Kind]+"/read", recipient, w.ID, nil, nil); code != 204 {
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
	if code := s.call(t, "GET", "/api/inbox?archived=1", recipient, w.ID, nil, &after); code != 200 || len(after) != len(before) {
		t.Fatalf("archive after read: %d %+v", code, after)
	}
	for i, item := range after {
		if item.ID != before[i].ID || item.ReadAt == nil || item.ResolvedAt == nil || *item.ResolvedAt != *before[i].ResolvedAt {
			t.Fatalf("read changed resolution: before=%+v after=%+v", before[i], item)
		}
	}
}
