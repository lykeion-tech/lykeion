package handler

import (
	"context"
	"testing"
)

type ratingItem struct {
	Column    string `json:"column"`
	Rating    *int   `json:"rating"`
	Dismissed bool   `json:"dismissed"`
}

// rated is a study owned by a@lab.org with one Hypotheses draw, offered or
// not.
type rated struct {
	s      *server
	owner  string
	wsID   string
	cardID string
}

func newRated(t *testing.T, offered bool) rated {
	t.Helper()
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var card struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "Why do cells age?"}, &card); code != 201 {
		t.Fatalf("create study: %d", code)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `WITH p AS (
		INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source)
		VALUES ($1, 'Team', '1', 'h', '{}', 'k', 'shipped') RETURNING id)
		INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at)
		SELECT $1, c.id, 'hypotheses', p.id, c.owner_id, $3, now() FROM card c, p WHERE c.id = $2`, w.ID, card.ID, offered); err != nil {
		t.Fatal(err)
	}
	return rated{s: s, owner: a, wsID: w.ID, cardID: card.ID}
}

func (r rated) list(t *testing.T, tok string) []ratingItem {
	t.Helper()
	var out []ratingItem
	if code := r.s.call(t, "GET", "/api/cards/"+r.cardID+"/ratings", tok, r.wsID, nil, &out); code != 200 {
		t.Fatalf("list ratings: %d", code)
	}
	return out
}

func (r rated) put(t *testing.T, tok string, body any) (int, ratingItem) {
	t.Helper()
	var out ratingItem
	code := r.s.call(t, "PUT", "/api/cards/"+r.cardID+"/ratings/hypotheses", tok, r.wsID, body, &out)
	return code, out
}

func TestTheOwnerRatesChangesAndClears(t *testing.T) {
	r := newRated(t, true)
	if got := r.list(t, r.owner); len(got) != 1 || got[0].Column != "hypotheses" || got[0].Rating != nil || got[0].Dismissed {
		t.Fatalf("before rating: %+v", got)
	}
	for _, want := range []int{3, 1} {
		if code, got := r.put(t, r.owner, map[string]any{"rating": want}); code != 200 || got.Rating == nil || *got.Rating != want {
			t.Fatalf("rate %d: %d %+v", want, code, got)
		}
	}
	if code, got := r.put(t, r.owner, map[string]any{"rating": nil}); code != 200 || got.Rating != nil {
		t.Fatalf("clear: %d %+v", code, got)
	}
	if got := r.list(t, r.owner); len(got) != 1 || got[0].Rating != nil {
		t.Fatalf("after clearing: %+v", got)
	}
}

func TestARatingMustBeOneToFour(t *testing.T) {
	r := newRated(t, true)
	if code, _ := r.put(t, r.owner, map[string]any{"rating": 2}); code != 200 {
		t.Fatalf("rate 2: %d", code)
	}
	for _, bad := range []any{0, 5, 2.5, "3", true} {
		if code, _ := r.put(t, r.owner, map[string]any{"rating": bad}); code != 400 {
			t.Fatalf("rating %v: %d, want 400", bad, code)
		}
	}
	if code, _ := r.put(t, r.owner, map[string]any{}); code != 400 {
		t.Fatalf("no rating field: %d, want 400", code)
	}
	if got := r.list(t, r.owner); got[0].Rating == nil || *got[0].Rating != 2 {
		t.Fatalf("a refused rating changed the stored one: %+v", got)
	}
}

func TestOnlyTheOwnerSeesAndSetsARating(t *testing.T) {
	r := newRated(t, true)
	b := r.s.member(t, r.owner, r.wsID, "b@lab.org", "member")
	if got := r.list(t, b); len(got) != 0 {
		t.Fatalf("another member sees %+v", got)
	}
	if code, _ := r.put(t, b, map[string]any{"rating": 4}); code != 403 {
		t.Fatalf("another member's rating: %d, want 403", code)
	}
	if code := r.s.call(t, "POST", "/api/cards/"+r.cardID+"/ratings/hypotheses/dismiss", b, r.wsID, nil, nil); code != 403 {
		t.Fatalf("another member's dismiss: %d, want 403", code)
	}
	if got := r.list(t, r.owner); got[0].Rating != nil || got[0].Dismissed {
		t.Fatalf("another member changed the offer: %+v", got)
	}
}

func TestDismissHidesTheQuestion(t *testing.T) {
	r := newRated(t, true)
	var got ratingItem
	if code := r.s.call(t, "POST", "/api/cards/"+r.cardID+"/ratings/hypotheses/dismiss", r.owner, r.wsID, nil, &got); code != 200 || !got.Dismissed {
		t.Fatalf("dismiss: %d %+v", code, got)
	}
	if list := r.list(t, r.owner); len(list) != 1 || !list[0].Dismissed {
		t.Fatalf("after dismissing: %+v", list)
	}
}

func TestANotOfferedDrawIsInvisible(t *testing.T) {
	r := newRated(t, false)
	if got := r.list(t, r.owner); len(got) != 0 {
		t.Fatalf("a missed draw is listed: %+v", got)
	}
	if code, _ := r.put(t, r.owner, map[string]any{"rating": 3}); code != 404 {
		t.Fatalf("rating a missed draw: %d, want 404", code)
	}
}
