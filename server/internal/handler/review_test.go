package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type reviewView struct {
	Columns []struct {
		Column   string
		Attempts []struct {
			Column, Decision string
			Attempt          int
		}
	}
}

func seedReviews(t *testing.T, s *server, ws, card string) {
	t.Helper()
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.Nil, func(tx *store.Tx) error {
		// Inserted out of board order; attempts 1 then 2 for grounding.
		for _, x := range []struct {
			col      string
			round    int32
			attempt  int
			decision string
		}{{"hypotheses", 1, 1, "pass"}, {"grounding", 1, 1, "redo"}, {"grounding", 2, 2, "pass"}} {
			b, _ := json.Marshal(map[string]any{"column": x.col, "attempt": x.attempt, "decision": x.decision})
			if _, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(card),
				BoardColumn: x.col, Round: x.round, Kind: "review", Role: "server", Body: b}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewEndpointListsAttemptsInOrder(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	seedReviews(t, s, w.ID, c.ID)
	var v reviewView
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", a, w.ID, nil, &v); code != 200 {
		t.Fatalf("code %d", code)
	}
	if len(v.Columns) != 4 || v.Columns[0].Column != "grounding" || v.Columns[1].Column != "hypotheses" || v.Columns[3].Column != "validation" {
		t.Fatalf("columns %+v", v.Columns)
	}
	g := v.Columns[0].Attempts
	if len(g) != 2 || g[0].Attempt != 1 || g[0].Decision != "redo" || g[1].Attempt != 2 || g[1].Decision != "pass" {
		t.Fatalf("grounding attempts %+v", g)
	}
	if len(v.Columns[1].Attempts) != 1 || len(v.Columns[2].Attempts) != 0 {
		t.Fatalf("%+v", v.Columns)
	}
}

func TestReviewEndpointIsWorkspaceScoped(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	var c card
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"question": "q"}, &c)
	seedReviews(t, s, wa.ID, c.ID)
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("B read A's review through B's workspace: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", b, wa.ID, nil, nil); code != 404 {
		t.Fatalf("B read A's review through A's workspace: %d", code)
	}
}
