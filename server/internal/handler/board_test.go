package handler

import (
	"lykeion/server/internal/jev"
	"strings"
	"testing"
)

func TestPeopleSteerButNeverMoveForward(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "budget_usd": 1}, &c)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'validation', round = 3 WHERE id = $1`, c.ID)

	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "reporting", "reason": "x"}, nil); code != 400 {
		t.Fatalf("sent forward: %d", code)
	}
	var out struct {
		BoardColumn string   `json:"board_column"`
		Stale       []string `json:"stale"`
		Round       int      `json:"round"`
		Flag        string   `json:"flag"`
		Ending      string   `json:"ending"`
		BudgetUsd   float64  `json:"budget_usd"`
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "hypotheses", "reason": "wrong rivals"}, &out); code != 200 {
		t.Fatalf("send-back: %d", code)
	}
	if out.BoardColumn != "hypotheses" || out.Round != 4 || len(out.Stale) != 2 {
		t.Fatalf("%+v", out)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET flag = 'out_of_budget' WHERE id = $1`, c.ID)
	s.call(t, "POST", "/api/cards/"+c.ID+"/budget", a, w.ID, map[string]float64{"usd": 40}, &out)
	if out.Flag != "" || out.BudgetUsd != 40 {
		t.Fatalf("budget: %+v", out)
	}
	s.call(t, "POST", "/api/cards/"+c.ID+"/stop", a, w.ID, map[string]string{"reason": "superseded by #2"}, &out)
	if out.BoardColumn != "done" || out.Ending != "stopped" {
		t.Fatalf("stop: %+v", out)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/resume", a, w.ID, nil, nil); code != 409 {
		t.Fatalf("resumed an ended card: %d", code)
	}
}

func TestSendBackReasonIsTheHandoffNote(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'hypotheses', round = 2 WHERE id = $1`, c.ID)
	var out struct {
		HandoffNote string `json:"handoff_note"`
		Rerun       string `json:"rerun"`
	}
	s.call(t, "POST", "/api/cards/"+c.ID+"/send-back", a, w.ID, map[string]string{"to": "grounding", "step": "contrarian", "reason": "missed the null result"}, &out)
	if out.HandoffNote != "missed the null result" || out.Rerun != "contrarian" {
		t.Fatalf("%+v", out)
	}
}

func TestDuplicateCards(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var first card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y?"}, &first)

	var dup struct {
		Error  string `json:"error"`
		CardID string `json:"card_id"`
		Number int    `json:"number"`
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "  does x\n raise   Y? "}, &dup); code != 409 {
		t.Fatalf("duplicate question: %d", code)
	}
	if dup.CardID != first.ID || dup.Number != first.Number || !strings.Contains(dup.Error, "#1") {
		t.Fatalf("%+v", dup)
	}
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"title": "DOES X RAISE Y?", "question": "other words"}, nil); code != 409 {
		t.Fatalf("duplicate title: %d", code)
	}
	var follow card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y?", "follows_from": []string{first.ID}}, &follow); code != 201 {
		t.Fatalf("a follow-up was refused: %d", code)
	}

	// two identical cards at once: exactly one is created
	codes := make(chan int, 2)
	for range 2 {
		go func() {
			codes <- s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Is Z lower in mice?"}, nil)
		}()
	}
	if c1, c2 := <-codes, <-codes; c1+c2 != 201+409 {
		t.Fatalf("concurrent creates: %d %d", c1, c2)
	}

	var out struct {
		Ending    string `json:"ending"`
		EndReason string `json:"end_reason"`
	}
	if code := s.call(t, "POST", "/api/cards/"+follow.ID+"/stop", a, w.ID, map[string]any{"duplicate_of": follow.Number}, nil); code != 400 {
		t.Fatalf("a card was stopped as a duplicate of itself: %d", code)
	}
	s.call(t, "POST", "/api/cards/"+follow.ID+"/stop", a, w.ID, map[string]any{"duplicate_of": first.Number}, &out)
	if out.Ending != "stopped" || out.EndReason != "duplicate of #1" {
		t.Fatalf("%+v", out)
	}
}

func TestSettingsAreOwnerOnly(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "admin")+"/accept", b, "", nil, nil)
	body := map[string]any{"settings": map[string]any{"jev": map[string]any{"enabled": true}}}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", b, "", body, nil); code != 403 {
		t.Fatalf("an admin turned on text egress: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", body, nil); code != 204 {
		t.Fatalf("owner: %d", code)
	}
	other := map[string]any{"settings": map[string]any{"jev": map[string]any{"enabled": true, "model": "jev-1.14.0"}}}
	var rejected struct {
		Error string `json:"error"`
	}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", other, &rejected); code != 400 {
		t.Fatalf("another Jev model was accepted: %d", code)
	}
	if !strings.Contains(rejected.Error, "pinned to "+jev.Model) {
		t.Fatalf("pin explanation: %q", rejected.Error)
	}
}

func TestSettingsAcceptPinnedModel(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	body := map[string]any{"settings": map[string]any{"jev": map[string]any{"model": jev.Model}}}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", body, nil); code != 204 {
		t.Fatalf("matching model: %d", code)
	}
}
