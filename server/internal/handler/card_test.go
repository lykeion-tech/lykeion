package handler

import (
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type card struct {
	ID          string
	Number      int
	Title       string
	BoardColumn string `json:"board_column"`
	OwnerID     string `json:"owner_id"`
}

func TestCardLifecycleBasics(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c1, c2 card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y in mouse liver?", "budget_usd": 20}, &c1); code != 201 {
		t.Fatalf("create: %d", code)
	}
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"title": "Second", "question": "q2", "follows_from": []string{c1.ID}}, &c2)
	if c1.Number != 1 || c2.Number != 2 || c1.BoardColumn != "backlog" || c1.Title == "" || c1.OwnerID != s.userID(t, a) {
		t.Fatalf("%+v %+v", c1, c2)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": " "}, nil); code != 400 {
		t.Fatalf("empty question: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "follows_from": []string{uuid.NewString()}}, nil); code != 400 {
		t.Fatalf("follows_from an unknown card: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "board_column": "validation"}, nil); code != 400 {
		t.Fatal("a card was created outside Backlog")
	}

	if code := s.call(t, "POST", "/api/cards/"+c1.ID+"/comments", a, w.ID, map[string]string{"body": "Focus on fasting state."}, nil); code != 201 {
		t.Fatalf("comment: %d", code)
	}
	var comments []map[string]any
	s.call(t, "GET", "/api/cards/"+c1.ID+"/comments", a, w.ID, nil, &comments)
	if len(comments) != 1 {
		t.Fatalf("%+v", comments)
	}

	var evs []struct {
		ID   int64
		Type string
	}
	s.call(t, "GET", "/api/cards/"+c1.ID+"/events?after=0", a, w.ID, nil, &evs)
	if len(evs) != 2 || evs[0].Type != "card.created" || evs[1].Type != "comment.created" {
		t.Fatalf("%+v", evs)
	}
	s.call(t, "GET", "/api/cards/"+c1.ID+"/events?after="+itoa(evs[0].ID), a, w.ID, nil, &evs)
	if len(evs) != 1 {
		t.Fatalf("after cursor: %+v", evs)
	}
}

func TestCardsAreScopedToTheirWorkspace(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	var c card
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"question": "q"}, &c)
	if code := s.call(t, "GET", "/api/cards/"+c.ID, b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("B read A's card through B's workspace: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards", b, wa.ID, nil, nil); code != 404 {
		t.Fatalf("B listed A's workspace: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards", a, "", nil, nil); code != 400 {
		t.Fatalf("missing workspace header: %d", code)
	}
}

func TestInbox(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	var item uuid.UUID
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
		VALUES ($1, $2, $3, 'escalated', '3 failed gates') RETURNING id`, w.ID, c.ID, s.userID(t, a)).Scan(&item)
	var inbox []map[string]any
	s.call(t, "GET", "/api/inbox", a, w.ID, nil, &inbox)
	if len(inbox) != 1 {
		t.Fatalf("%+v", inbox)
	}
	if code := s.call(t, "POST", "/api/inbox/"+item.String()+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("read: %d", code)
	}
	if code := s.call(t, "POST", "/api/inbox/"+item.String()+"/read", a, w.ID, nil, nil); code != 404 {
		t.Fatalf("read twice: %d", code)
	}
}

func TestEmptyCollectionResponsesAreJSONArrays(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "empty@lab.org")
	w := s.workspace(t, a, "empty-lab")
	assertBody := func(method, path string) {
		t.Helper()
		req, err := http.NewRequest(method, s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+a)
		req.Header.Set("X-Workspace-ID", w.ID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || string(body) != "[]\n" {
			t.Errorf("%s %s = status %d body %q, want 200 and []", method, path, resp.StatusCode, body)
		}
	}

	assertBody("GET", "/api/cards")
	assertBody("GET", "/api/inbox")

	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c); code != http.StatusCreated {
		t.Fatalf("create card: %d", code)
	}
	assertBody("GET", "/api/cards/"+c.ID+"/comments")

	var events []struct {
		ID int64
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/events?after=0", a, w.ID, nil, &events); code != http.StatusOK || len(events) == 0 {
		t.Fatalf("read initial events: status %d events %v", code, events)
	}
	assertBody("GET", "/api/cards/"+c.ID+"/events?after="+itoa(events[len(events)-1].ID))
}
