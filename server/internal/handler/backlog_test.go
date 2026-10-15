package handler

import (
	"testing"

	"github.com/google/uuid"
	"time"
)

type backlogCard struct {
	ID          string     `json:"id"`
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	Question    string     `json:"question"`
	BudgetUsd   float64    `json:"budget_usd"`
	Deadline    *time.Time `json:"deadline"`
	FollowsFrom []string   `json:"follows_from"`
	Position    float64    `json:"position"`
}

func TestNewCardsGoToTheTopOfBacklog(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c1, c2, c3 backlogCard
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q1"}, &c1)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q2"}, &c2)
	if c1.Position != 0 || c2.Position != -1 {
		t.Fatalf("positions %v %v", c1.Position, c2.Position)
	}
	// Only Backlog counts: a started card's position is not the top.
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding', position = -50 WHERE id = $1`, c2.ID)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q3"}, &c3)
	if c3.Position != -1 {
		t.Fatalf("third position %v", c3.Position)
	}
}

func TestBacklogCardsCanBeEditedReorderedAndDeleted(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c1, c2 backlogCard
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q1", "budget_usd": 5}, &c1)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q2"}, &c2)
	path := "/api/cards/" + c1.ID

	var out backlogCard
	if code := s.call(t, "PATCH", "/api/cards/"+c2.ID, a, w.ID, map[string]any{"position": 0.5}, &out); code != 200 || out.Position != 0.5 {
		t.Fatalf("reorder: %d %+v", code, out)
	}
	deadline := "2026-12-01T00:00:00Z"
	body := map[string]any{"title": " New title ", "question": "q1, sharper", "budget_usd": 7, "deadline": deadline, "follows_from": []string{c2.ID}}
	if code := s.call(t, "PATCH", path, a, w.ID, body, &out); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	if out.Title != "New title" || out.Question != "q1, sharper" || out.BudgetUsd != 7 || out.Deadline == nil ||
		len(out.FollowsFrom) != 1 || out.FollowsFrom[0] != c2.ID || out.Position != 0 {
		t.Fatalf("edited: %+v", out)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"deadline": nil}, &out); code != 200 || out.Deadline != nil || out.BudgetUsd != 7 {
		t.Fatalf("clear deadline: %d %+v", code, out)
	}
	for _, bad := range []map[string]any{{}, {"title": " "}, {"question": ""}, {"budget_usd": -1}, {"follows_from": []string{c1.ID}}, {"follows_from": []string{uuid.NewString()}}, {"board_column": "done"}} {
		if code := s.call(t, "PATCH", path, a, w.ID, bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
	var types []string
	rows, err := s.Env.Owner.Query(t.Context(), `SELECT type FROM event WHERE workspace_id = $1 AND type IN ('card.edited', 'card.reordered') ORDER BY id`, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var typ string
		rows.Scan(&typ)
		types = append(types, typ)
	}
	rows.Close()
	if len(types) != 3 || types[0] != "card.reordered" || types[1] != "card.edited" || types[2] != "card.edited" {
		t.Fatalf("events %v", types)
	}

	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'grounding' WHERE id = $1`, c1.ID)
	var refused struct{ Error string }
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"title": "late"}, &refused); code != 409 || refused.Error != "only a card in Backlog can be edited or deleted" {
		t.Fatalf("edit outside Backlog: %d %+v", code, refused)
	}
	if code := s.call(t, "DELETE", path, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete outside Backlog: %d", code)
	}

	if code := s.call(t, "DELETE", "/api/cards/"+c2.ID, a, w.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards/"+c2.ID, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("deleted card still read: %d", code)
	}
	var deleted int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'card.deleted' AND card_id IS NULL AND payload->>'card_id' = $2 AND (payload->>'number')::int = $3`, w.ID, c2.ID, c2.Number).Scan(&deleted); err != nil || deleted != 1 {
		t.Fatalf("card.deleted events: %d %v", deleted, err)
	}
	if code := s.call(t, "GET", path, a, w.ID, nil, &out); code != 200 || len(out.FollowsFrom) != 1 || out.FollowsFrom[0] != c2.ID {
		t.Fatalf("deleted predecessor was not retained: %d %+v", code, out)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+c2.ID, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestBacklogEditsStayInTheirWorkspace(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	var c backlogCard
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"question": "q"}, &c)
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID, b, wb.ID, map[string]any{"title": "mine"}, nil); code != 404 {
		t.Fatalf("edit across workspaces: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+c.ID, b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("delete across workspaces: %d", code)
	}
}
