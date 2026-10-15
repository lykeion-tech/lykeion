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
