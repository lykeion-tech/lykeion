package handler

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

type pred struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func (s *server) createWith(t *testing.T, tok, wsID string, body map[string]any) (int, map[string]any) {
	t.Helper()
	var out map[string]any
	code := s.call(t, "POST", "/api/cards", tok, wsID, body, &out)
	return code, out
}

func TestAStudyThatRunsExperimentationNeedsPredicates(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q"})
	if code != 400 || out["error"] != "a study that runs Experimentation needs at least one pre-registered result" {
		t.Fatalf("no predicates: %d %v", code, out)
	}
	want := []pred{{Name: "effect", Text: "effect > 0.2"}}
	code, out = s.createWith(t, a, w.ID, map[string]any{"question": "q", "predicates": want})
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	var got struct{ Predicates []pred }
	s.call(t, "GET", "/api/cards/"+out["id"].(string), a, w.ID, nil, &got)
	if len(got.Predicates) != 1 || got.Predicates[0] != want[0] {
		t.Fatalf("got %v", got.Predicates)
	}
}

func TestPredicatesAreRefusedOutsideExperimentation(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "scope": map[string]string{"from": "grounding", "to": "hypotheses"},
		"predicates": []pred{{Name: "effect", Text: "effect > 0"}}})
	if code != 400 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestBadPredicatesAreRefused(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ok := pred{"effect", "effect > 0"}
	nine := make([]pred, 9)
	for i := range nine {
		nine[i] = pred{"p" + string(rune('a'+i)), "effect > 0"}
	}
	cases := map[string]struct {
		in   []pred
		want string
	}{
		"nine":       {nine, "at most 8"},
		"no name":    {[]pred{{Name: "", Text: "effect > 0"}}, "pre-registered result 1"},
		"long name":  {[]pred{{Name: strings.Repeat("n", 81), Text: "effect > 0"}}, strings.Repeat("n", 81)},
		"duplicate":  {[]pred{ok, ok}, "effect"},
		"bad text":   {[]pred{{Name: "bad", Text: "effect == 0"}}, "bad"},
		"long text":  {[]pred{{Name: "long", Text: "effect > 0 " + strings.Repeat(" ", 512)}}, "long"},
		"empty text": {[]pred{{Name: "blank", Text: ""}}, "blank"},
		"line break": {[]pred{{Name: "a\nb", Text: "effect > 0"}}, "line break or control character"},
	}
	for name, c := range cases {
		code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q " + name, "predicates": c.in})
		msg, _ := out["error"].(string)
		if code != 400 || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %d %q", name, code, msg)
		}
	}
}

func TestALoneExperimentationTeamNeedsPredicates(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	body := map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"experimentation": s.builtInID(t, w.ID, "experimentation")})}
	if code, out := s.createWith(t, a, w.ID, body); code != 400 {
		t.Fatalf("%d %v", code, out)
	}
	body["predicates"] = []pred{{Name: "effect", Text: "effect > 0"}}
	if code, out := s.createWith(t, a, w.ID, body); code != 201 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestPredicatesCannotBeChanged(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "predicates": []pred{{Name: "effect", Text: "effect > 0"}}})
	if code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	id := out["id"].(string)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET predicates = '[]' WHERE id = $1`, id); err == nil {
		t.Fatal("predicates were changed")
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET title = 'x' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestAutoresearchStudiesStartWithoutPredicates(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	if n := s.count(t, `SELECT count(*) FROM card WHERE id = $1 AND predicates = '[]'::jsonb`, uuid.MustParse(c.ID)); n != 1 {
		t.Fatalf("predicates not empty: %d", n)
	}
}

// testPredicates is the pre-registration a test sends to open an end-to-end study.
var testPredicates = []pred{{Name: "effect", Text: "effect > 0"}}

func TestTheStudyViewCarriesBindingOutcomes(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "predicates": []pred{{Name: "effect", Text: "effect > 0.2"}}})
	if code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	id := out["id"].(string)
	var got struct {
		Preregistration []map[string]any `json:"preregistration"`
	}
	s.call(t, "GET", "/api/cards/"+id, a, w.ID, nil, &got)
	if got.Preregistration == nil || len(got.Preregistration) != 0 {
		t.Fatalf("before scoring: %v", got.Preregistration)
	}
	for i, body := range []string{
		`{"verdict":"low","predicates":[{"name":"effect","text":"effect > 0.2","outcome":"missed","round":1,"run":"x1","values":{"effect":0.12}}]}`,
		`{"verdict":"high","predicates":[{"name":"effect","text":"effect > 0.2","outcome":"held","round":2,"run":"x2","values":{"effect":0.5}}]}`,
	} {
		if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			VALUES ($1, $2, 'experimentation', $3, 'preregistration', 'server', $4, $5)`, w.ID, id, i+1, "r"+string(rune('1'+i)), body); err != nil {
			t.Fatal(err)
		}
	}
	s.call(t, "GET", "/api/cards/"+id, a, w.ID, nil, &got)
	if len(got.Preregistration) != 1 || got.Preregistration[0]["outcome"] != "missed" || got.Preregistration[0]["run"] != "x1" {
		t.Fatalf("after scoring: %v", got.Preregistration)
	}
}

func TestHeavyPredicatesAreNotAServerError(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var in []pred
	for i := 0; i < 8; i++ {
		name := strings.Repeat("𝔼", 79) + string(rune('a'+i))
		text := "effect > 0" + strings.Repeat(" ", 502)
		in = append(in, pred{name, text})
	}
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q heavy", "predicates": in})
	if code != 201 && code != 400 {
		t.Fatalf("%d %v", code, out)
	}
}
