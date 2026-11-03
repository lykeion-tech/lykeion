package handler

import (
	"encoding/json"
	"testing"
	"time"

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
			Round            int
			At               string
			Items, Missed    []struct {
				Key, Label, Quote string
				Cites             []string
			}
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
			kind  string
			round int32
			body  any
		}{
			{"validation_status", 1, map[string]any{"hypothesis": "H1", "status": "valid", "reasons": []string{}, "cites": []string{}, "final": true}},
			{"validation_status", 2, map[string]any{"hypothesis": "H1", "status": "invalid", "reasons": []string{"the value did not reproduce"}, "cites": []string{}, "final": true}},
			{"objection", 1, map[string]any{"target_key": "k", "label": "old", "type": "leakage", "problem": "p", "server": "ok", "checks": []any{}, "upheld": true, "same_family": false}},
			{"objection", 2, map[string]any{"target_key": "k", "label": "H1", "type": "leakage", "problem": "p", "server": "ok", "checks": []any{}, "upheld": true, "same_family": true}},
			{"review", 2, map[string]any{"column": "validation", "attempt": 2, "items": []any{}, "decision": "ask",
				"objector": map[string]any{"ran": false, "reason": "the objector did not run: its run failed (agent error)", "same_family": true}}},
			{"rerun", 2, map[string]any{"reproduced": map[string]bool{"v": false}, "notes": map[string]string{"v": "off by 3"}}},
		} {
			b, _ := json.Marshal(x.body)
			if _, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(c.ID),
				BoardColumn: "validation", Round: x.round, Kind: x.kind, Role: "server", Body: b}); err != nil {
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
			Objector   *struct {
				Ran    bool
				Reason string
			}
		}
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", a, w.ID, nil, &v); code != 200 {
		t.Fatalf("code %d", code)
	}
	if len(v.Columns[0].Statuses) != 0 || v.Columns[0].Mode != "" {
		t.Fatalf("grounding carries validation fields: %+v", v.Columns[0])
	}
	val := v.Columns[3]
	if val.Mode != "claims" || len(val.Statuses) != 1 || val.Statuses[0].Status != "invalid" || !val.Statuses[0].Final ||
		len(val.Objections) != 1 || val.Objections[0].Label != "H1" || val.Rerun.Notes["v"] != "off by 3" || val.Rerun.Reproduced["v"] || !val.SameFamily ||
		val.Objector == nil || val.Objector.Ran || val.Objector.Reason != "the objector did not run: its run failed (agent error)" {
		t.Fatalf("validation %+v", val)
	}
}

// Missing enrichment or filtering out superseded records loses the evidence
// behind earlier review attempts.
func TestCardReviewAddsRoundTimeAndItemText(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa, wb := s.workspace(t, a, "lab-a"), s.workspace(t, b, "lab-b")
	var c card
	s.call(t, "POST", "/api/cards", a, wa.ID, map[string]any{"question": "q"}, &c)
	var reviewAt time.Time
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(wa.ID), uuid.Nil, func(tx *store.Tx) error {
		hypothesis, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(c.ID),
			BoardColumn: "hypotheses", Round: 1, Kind: "hypothesis", Role: "hypothesist", Key: "r2",
			Body: json.RawMessage(`{"text":"Fertilizer makes seedlings taller.","cites":["r1"],"label":"H1"}`)})
		if err != nil {
			return err
		}
		verdict, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(c.ID),
			BoardColumn: "validation", Round: 2, Kind: "verdict", Role: "validator", Key: "r3",
			Body: json.RawMessage(`{"claim":"C","source":"paper.pdf","quote":"q","label":"V1"}`)})
		if err != nil {
			return err
		}
		for _, item := range []db.Record{hypothesis, verdict} {
			body, err := json.Marshal(map[string]any{"column": item.BoardColumn, "attempt": item.Round, "decision": "pass",
				"items":  []any{map[string]any{"key": item.Key, "label": "reviewed item", "checks": []any{}}},
				"missed": []any{map[string]any{"key": "", "label": "doi:10.1/x", "checks": []any{}}}})
			if err != nil {
				return err
			}
			rec, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(c.ID),
				BoardColumn: item.BoardColumn, Round: item.Round, Kind: "review", Role: "server", Body: body})
			if err != nil {
				return err
			}
			if item.BoardColumn == "hypotheses" {
				reviewAt = rec.CreatedAt
			}
		}
		return tx.Q.SupersedeRecord(t.Context(), db.SupersedeRecordParams{WorkspaceID: tx.WS, ID: hypothesis.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	var v reviewView
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", a, wa.ID, nil, &v); code != 200 {
		t.Fatalf("code %d", code)
	}
	if len(v.Columns) != 4 || len(v.Columns[1].Attempts) != 1 || len(v.Columns[3].Attempts) != 1 {
		t.Fatalf("columns %+v", v.Columns)
	}
	h := v.Columns[1].Attempts[0]
	if h.Round != 1 {
		t.Fatalf("hypothesis review round = %d, want 1", h.Round)
	}
	at, err := time.Parse(time.RFC3339Nano, h.At)
	if err != nil || !at.Equal(reviewAt) {
		t.Fatalf("review at = %q, want %s (parse error %v)", h.At, reviewAt, err)
	}
	if len(h.Items) != 1 || h.Items[0].Quote != "Fertilizer makes seedlings taller." || len(h.Items[0].Cites) != 1 || h.Items[0].Cites[0] != "r1" {
		t.Fatalf("hypothesis items %+v", h.Items)
	}
	verdict := v.Columns[3].Attempts[0]
	if verdict.Round != 2 || len(verdict.Items) != 1 || verdict.Items[0].Quote != "C" || len(verdict.Items[0].Cites) != 1 || verdict.Items[0].Cites[0] != "paper.pdf" {
		t.Fatalf("verdict review %+v", verdict)
	}
	for _, attempt := range []int{1, 3} {
		missed := v.Columns[attempt].Attempts[0].Missed
		if len(missed) != 1 || missed[0].Quote != "doi:10.1/x" || missed[0].Cites == nil || len(missed[0].Cites) != 0 {
			t.Fatalf("missed items %+v", missed)
		}
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("B read A's review through B's workspace: %d", code)
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/review", b, wa.ID, nil, nil); code != 404 {
		t.Fatalf("B read A's review through A's workspace: %d", code)
	}
}
