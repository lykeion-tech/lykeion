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

func TestReviewEndpointAnswersValidationRecords(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(w.ID), uuid.Nil, func(tx *store.Tx) error {
		for _, x := range []struct {
			kind string
			body any
		}{
			{"validation_status", map[string]any{"hypothesis": "H1", "status": "invalid", "reasons": []string{"the value did not reproduce"}, "cites": []string{}, "final": true}},
			{"objection", map[string]any{"target_key": "k", "label": "H1", "type": "leakage", "problem": "p", "server": "ok", "checks": []any{}, "upheld": true, "same_family": true}},
			{"rerun", map[string]any{"reproduced": map[string]bool{"v": false}, "notes": map[string]string{"v": "off by 3"}}},
		} {
			b, _ := json.Marshal(x.body)
			if _, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(c.ID),
				BoardColumn: "validation", Round: 1, Kind: x.kind, Role: "server", Body: b}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Columns []struct {
			Column   string
			Mode     string
			Statuses []struct {
				Hypothesis, Status string
				Reasons            []string
				Final              bool
			}
			Objections []struct{ Label string }
			Rerun      struct {
				Reproduced map[string]bool
				Notes      map[string]string
			}
			SameFamily bool `json:"same_family"`
		}
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", a, w.ID, nil, &v); code != 200 {
		t.Fatalf("code %d", code)
	}
	val := v.Columns[3]
	if val.Mode != "claims" || len(val.Statuses) != 1 || val.Statuses[0].Status != "invalid" || !val.Statuses[0].Final ||
		len(val.Objections) != 1 || val.Rerun.Notes["v"] != "off by 3" || val.Rerun.Reproduced["v"] || !val.SameFamily {
		t.Fatalf("validation %+v", val)
	}
}
