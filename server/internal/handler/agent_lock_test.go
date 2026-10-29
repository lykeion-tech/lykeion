package handler

import (
	"fmt"
	"testing"
)

func (s *server) startStudy(t *testing.T, tok, wsID, agent, question string) string {
	t.Helper()
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", tok, wsID, map[string]any{"question": question, "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "grounding", "to": "grounding"}}, &c); code != 201 {
		t.Fatalf("start study: %d", code)
	}
	return c.ID
}

func (s *server) rename(t *testing.T, tok, wsID, agent string) (int, string) {
	t.Helper()
	var e struct{ Error string }
	code := s.call(t, "PATCH", "/api/agents/"+agent, tok, wsID, map[string]string{"name": "x"}, &e)
	return code, e.Error
}

func TestAnAgentInUseCannotChange(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.Env.Owner.Exec(t.Context(), `UPDATE "user" SET name = 'Ada' WHERE email = 'a@lab.org'`)
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	card := s.startStudy(t, a, w.ID, agent, "q")
	want := "Used by study #1 (Ada). It can change once they finish or their owners delete them."
	if code, msg := s.rename(t, a, w.ID, agent); code != 409 || msg != want {
		t.Fatalf("patch: %d %q", code, msg)
	}
	var e struct{ Error string }
	if code := s.call(t, "POST", "/api/agents/"+agent+"/archive", a, w.ID, nil, &e); code != 409 || e.Error != want {
		t.Fatalf("archive: %d %q", code, e.Error)
	}
	if code := s.call(t, "DELETE", "/api/cards/"+card, a, w.ID, nil, nil); code/100 != 2 {
		t.Fatalf("delete: %d", code)
	}
	if code, msg := s.rename(t, a, w.ID, agent); code != 200 {
		t.Fatalf("patch after delete: %d %q", code, msg)
	}
}

func TestColleaguesStudiesAndAutoresearchesHoldASharedAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	b := s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil)
	s.Env.Owner.Exec(t.Context(), `UPDATE "user" SET name = 'Bea' WHERE email = 'b@lab.org'`)
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	s.startStudy(t, b, w.ID, agent, "one")
	blocked := s.startStudy(t, b, w.ID, agent, "two")
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET state = 'blocked' WHERE id = $1`, blocked)
	mixed := s.idleCard(t, b, w.ID, "three")
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET assignee_type = 'team', assignee_id = $2 WHERE id = $1`, mixed.ID, agent)
	var ar struct{ ID string }
	if code := s.call(t, "POST", "/api/autoresearch", b, w.ID, map[string]any{"title": "Digest", "execution_mode": "run_only",
		"assignee": map[string]any{"type": "agent", "id": agent}}, &ar); code != 201 {
		t.Fatalf("autoresearch: %d", code)
	}
	want := `Used by study #1 (Bea), study #2 (Bea), study #3 (Bea) and Autoresearch "Digest" (Bea). It can change once they finish or their owners delete them.`
	if code, msg := s.rename(t, a, w.ID, agent); code != 409 || msg != want {
		t.Fatalf("patch: %d %q", code, msg)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET state = 'done'`)
	if code := s.call(t, "DELETE", "/api/autoresearch/"+ar.ID, b, w.ID, nil, nil); code/100 != 2 {
		t.Fatalf("delete autoresearch: %d", code)
	}
	if code, msg := s.rename(t, a, w.ID, agent); code != 200 {
		t.Fatalf("patch after: %d %q", code, msg)
	}
}

func TestAFinishedOrEndedStudyDoesNotHoldTheAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	one, two := s.startStudy(t, a, w.ID, agent, "one"), s.startStudy(t, a, w.ID, agent, "two")
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET state = 'done' WHERE id = $1`, one)
	s.Env.Owner.Exec(t.Context(), `UPDATE card SET ending = 'stopped' WHERE id = $1`, two)
	if code, msg := s.rename(t, a, w.ID, agent); code != 200 {
		t.Fatalf("%d %q", code, msg)
	}
}

func TestMoreThanFiveUsesEndWithTheCount(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	for i := 1; i <= 7; i++ {
		s.startStudy(t, a, w.ID, agent, fmt.Sprintf("q%d", i))
	}
	want := fmt.Sprintf("Used by study #1 (a@lab.org), study #2 (a@lab.org), study #3 (a@lab.org), study #4 (a@lab.org), study #5 (a@lab.org) and 2 more. It can change once they finish or their owners delete them.")
	if code, msg := s.rename(t, a, w.ID, agent); code != 409 || msg != want {
		t.Fatalf("%d %q", code, msg)
	}
}
