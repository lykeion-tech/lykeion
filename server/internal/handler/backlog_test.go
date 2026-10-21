package handler

import (
	"testing"
	"time"
)

type backlogCard struct {
	ID          string     `json:"id"`
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	Question    string     `json:"question"`
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

func TestADeletedStudyLeavesItsEventAndItsFollowers(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c1, c2 backlogCard
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q1"}, &c1)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q2", "follows_from": []string{c1.ID}}, &c2)
	if code := s.call(t, "DELETE", "/api/cards/"+c1.ID, a, w.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards/"+c1.ID, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("deleted card still read: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'card.deleted' AND card_id IS NULL AND payload->>'card_id' = $2 AND (payload->>'number')::int = $3`, w.ID, c1.ID, c1.Number); n != 1 {
		t.Fatalf("card.deleted events: %d", n)
	}
	var out backlogCard
	if code := s.call(t, "GET", "/api/cards/"+c2.ID, a, w.ID, nil, &out); code != 200 || len(out.FollowsFrom) != 1 || out.FollowsFrom[0] != c1.ID {
		t.Fatalf("deleted predecessor was not retained: %d %+v", code, out)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+c1.ID, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestDeletesStayInTheirWorkspace(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	c := s.idleCard(t, a, wa.ID, "q")
	if code := s.call(t, "DELETE", "/api/cards/"+c.ID, b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("delete across workspaces: %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM card WHERE id = $1", c.ID); n != 1 {
		t.Fatal("deleted across workspaces")
	}
}
