package handler

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/testutil"
)

// saveSkills sets the leader's skills and returns the status and any error.
func (s *server) saveSkills(t *testing.T, tok, wsID, id string, skills ...string) (int, string) {
	t.Helper()
	var e struct{ Error string }
	c := s.call(t, "POST", "/api/teams/"+id+"/skills", tok, wsID, map[string]any{"member": "pi", "skills": append([]string{}, skills...)}, &e)
	return c, e.Error
}

func TestSavingATeamReplacesItInPlace(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "skill-editor@lab.org")
	w := s.workspace(t, a, "skills")
	b := s.member(t, a, w.ID, "colleague@lab.org", "member")
	var source struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "editable-team"}), &source)
	var old struct{ Manifest pack.Manifest }
	s.call(t, "GET", "/api/teams/"+source.ID, a, w.ID, nil, &old)
	var made struct{ ID string }
	if c := s.call(t, "POST", "/api/teams/"+source.ID+"/skills", a, w.ID, map[string]any{"member": "pi", "skills": []string{"aeon"}}, &made); c != 200 {
		t.Fatalf("save skills: %d", c)
	}
	if made.ID != source.ID {
		t.Fatalf("saved as %s, want %s", made.ID, source.ID)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack WHERE name = 'editable-team'"); n != 1 {
		t.Fatalf("%d rows for the Team", n)
	}
	var updated struct{ Manifest pack.Manifest }
	s.call(t, "GET", "/api/teams/"+source.ID, a, w.ID, nil, &updated)
	if !slices.Equal(updated.Manifest.Leader.Skills, []string{"aeon"}) {
		t.Fatalf("selection not saved: %v", updated.Manifest.Leader.Skills)
	}
	if !reflect.DeepEqual(updated.Manifest.Auditor.Agent, old.Manifest.Auditor.Agent) || !reflect.DeepEqual(updated.Manifest.Leader.Agent, old.Manifest.Leader.Agent) {
		t.Fatal("agent definitions changed")
	}
	if c := s.call(t, "GET", "/api/teams/"+source.ID, b, w.ID, nil, nil); c != 404 {
		t.Fatalf("private Team visible: %d", c)
	}
	if got := s.storeList(t, a, w.ID, ""); len(got) != 1 || got[0].ID != source.ID || !got[0].Added {
		t.Fatalf("saved Team not added: %+v", got)
	}
	if c, _ := s.saveSkills(t, a, w.ID, source.ID, "nonexistent-skill"); c != 400 {
		t.Fatalf("unavailable skill: %d", c)
	}
	if c := s.call(t, "PUT", "/api/availability/skill/bundle:anndata", a, w.ID, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
		t.Fatalf("disable skill: %d", c)
	}
	if c, _ := s.saveSkills(t, a, w.ID, source.ID, "anndata"); c != 200 {
		t.Fatalf("skill hidden by old availability setting: %d", c)
	}
	s.call(t, "GET", "/api/teams/"+source.ID, a, w.ID, nil, &updated)
	if !slices.Equal(updated.Manifest.Leader.Skills, []string{"anndata"}) {
		t.Fatalf("skill not saved: %v", updated.Manifest.Leader.Skills)
	}
	if c := s.call(t, "POST", "/api/teams/"+source.ID+"/skills", a, w.ID, map[string]any{"member": "unknown", "skills": []string{}}, nil); c != 400 {
		t.Fatalf("unknown member: %d", c)
	}
}

func TestAUsedTeamCannotBeEdited(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation")
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "validation": r})}, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c, e := s.saveSkills(t, a, w.ID, r); c != 409 || !strings.Contains(e, "Used by 1 study") {
		t.Fatalf("Team on an unreached column: %d %q", c, e)
	}
	if c := s.call(t, "PATCH", "/api/teams/"+r, a, w.ID, map[string]string{"avatar_url": "emoji:🧪"}, nil); c != 409 {
		t.Fatalf("icon while Team in use: %d", c)
	}
	ar := s.addedTeam(t, a, w.ID, "ar", "grounding")
	s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card",
		"assignee": teamAssignee(map[string]string{"grounding": ar})})
	if c, e := s.saveSkills(t, a, w.ID, ar); c != 409 || !strings.Contains(e, "Used by 1 Autoresearch") {
		t.Fatalf("Team an Autoresearch names: %d %q", c, e)
	}
}

func TestLockCountsOnlyUnfinishedColumns(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g, r, r2 := s.addedTeam(t, a, w.ID, "g", "grounding"), s.addedTeam(t, a, w.ID, "r", "validation"), s.addedTeam(t, a, w.ID, "r2", "validation")
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"grounding": g, "validation": r})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if c, e := s.saveSkills(t, a, w.ID, g); c != 409 {
		t.Fatalf("Team on the current column: %d %q", c, e)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'hypotheses' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if c, e := s.saveSkills(t, a, w.ID, g); c != 200 {
		t.Fatalf("Team only on a finished column: %d %q", c, e)
	}
	if c, _ := s.saveSkills(t, a, w.ID, r); c != 409 {
		t.Fatalf("Team on an unreached column: %d", c)
	}
	for _, tc := range []struct {
		set  string
		want int
	}{
		{`state = 'blocked'`, 409},
		{`ending = 'stopped'`, 200},
		{`ending = '', board_column = 'done', state = 'done'`, 200},
	} {
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET `+tc.set+` WHERE id = $1`, c.ID); err != nil {
			t.Fatal(err)
		}
		if got, e := s.saveSkills(t, a, w.ID, r); got != tc.want {
			t.Fatalf("%s: %d %q", tc.set, got, e)
		}
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET ending = '', board_column = 'hypotheses', state = 'working' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PATCH", "/api/cards/"+c.ID+"/teams", a, w.ID, map[string]string{"validation": r2}, nil); code != 200 {
		t.Fatalf("swap: %d", code)
	}
	if c, e := s.saveSkills(t, a, w.ID, r); c != 200 {
		t.Fatalf("after swapping it off: %d %q", c, e)
	}
}

func TestBuiltInTeamsAreReadOnly(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.builtInID(t, w.ID, "grounding")
	if c, e := s.saveSkills(t, a, w.ID, id); c != 403 || e != "Built-in Teams can't be changed; duplicate it instead" {
		t.Fatalf("save on a built-in Team: %d %q", c, e)
	}
	if c := s.call(t, "PATCH", "/api/teams/"+id, a, w.ID, map[string]string{"avatar_url": "emoji:🧪"}, nil); c != 403 {
		t.Fatalf("icon on a built-in Team: %d", c)
	}
}
