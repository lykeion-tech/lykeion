package handler

import "testing"

func TestTeamRatingsAreRemoved(t *testing.T) {
	s := newServer(t)
	user := s.signIn(t, "rating-removal@example.test")
	ws := s.workspace(t, user, "rating-removal")
	card := s.idleCard(t, user, ws.ID, "Question")
	var exists bool
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT to_regclass('public.team_rating') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("team_rating table still exists")
	}
	for _, method := range []string{"GET", "PUT", "POST"} {
		path := "/api/cards/" + card.ID + "/ratings"
		if method != "GET" {
			path += "/grounding"
		}
		if method == "POST" {
			path += "/dismiss"
		}
		if got := s.call(t, method, path, user, ws.ID, nil, nil); got != 404 {
			t.Errorf("%s %s: got %d, want 404", method, path, got)
		}
	}
}
