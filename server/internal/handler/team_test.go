package handler

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type storeRow struct {
	ID, Name, Domain, Stage string
	Added                   bool
}

func (s *server) storeList(t *testing.T, tok, wsID, query string) []storeRow {
	t.Helper()
	var rows []storeRow
	if c := s.call(t, "GET", "/api/teams"+query, tok, wsID, nil, &rows); c != 200 {
		t.Fatalf("store: %d", c)
	}
	return rows
}

func (s *server) newAgent(t *testing.T, tok, wsID, name, harness string) string {
	t.Helper()
	var a struct{ ID string }
	if c := s.call(t, "POST", "/api/agents", tok, wsID, map[string]string{"name": name, "harness": harness, "instructions": "Be " + name}, &a); c != 201 {
		t.Fatalf("agent %s: %d", name, c)
	}
	return a.ID
}

func formFor(leader, auditor, member string) map[string]any {
	slot := func(name, id string) map[string]any {
		return map[string]any{"name": name, "role": "Does the " + name + " work.", "agent_id": id}
	}
	return map[string]any{"name": "formteam", "description": "d", "domain": "compbio", "stage": "grounding",
		"leader": slot("pi", leader), "auditor": slot("auditor", auditor), "members": []any{slot("scout", member)},
		"criteria": []string{}, "steps": 5}
}

func TestTheStoreFiltersByDomainAndStage(t *testing.T) {
	s := newServer(t)
	shipped := shippedFS(t,
		testutil.Pack(t, testutil.PackOpts{Name: "bio", Domain: "compbio", Stage: "grounding"}),
		testutil.Pack(t, testutil.PackOpts{Name: "chem", Domain: "compchem", Stage: "hypotheses"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	wsID, ana := uuid.MustParse(w.ID), uuid.MustParse(s.userID(t, a))
	err := s.H.Store.InWorkspace(context.Background(), wsID, ana, func(tx *store.Tx) error {
		for _, n := range []string{"bio", "chem"} {
			if _, err := shipOne(context.Background(), tx, s.H.Blobs, shipped, n); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.storeList(t, a, w.ID, ""); len(got) != 2 {
		t.Fatalf("no filter: %+v", got)
	}
	if got := s.storeList(t, a, w.ID, "?domain=compbio"); len(got) != 1 || got[0].Name != "bio" {
		t.Fatalf("domain filter: %+v", got)
	}
	if got := s.storeList(t, a, w.ID, "?stage=hypotheses"); len(got) != 1 || got[0].Name != "chem" {
		t.Fatalf("stage filter: %+v", got)
	}
}

// versionKeys lists the paths of any "version" or "versions" key in v.
func versionKeys(v any, path string) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == "version" || k == "versions" {
				out = append(out, path+"."+k)
			}
			out = append(out, versionKeys(child, path+"."+k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, versionKeys(child, path+"[]")...)
		}
	}
	return out
}

func hasKey(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == key || hasKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if hasKey(child, key) {
				return true
			}
		}
	}
	return false
}

func TestNoVersionInTeamResponses(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	lead, aud, mem := s.newAgent(t, a, w.ID, "lead", "claude"), s.newAgent(t, a, w.ID, "aud", "codex"), s.newAgent(t, a, w.ID, "mem", "claude")
	var created map[string]any
	if c := s.call(t, "POST", "/api/teams", a, w.ID, formFor(lead, aud, mem), &created); c != 201 {
		t.Fatalf("create: %d", c)
	}
	var list []any
	var detail map[string]any
	s.call(t, "GET", "/api/teams", a, w.ID, nil, &list)
	s.call(t, "GET", "/api/teams/"+created["id"].(string), a, w.ID, nil, &detail)
	var study, card map[string]any
	if c := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &study); c != 201 {
		t.Fatalf("study: %d", c)
	}
	s.call(t, "GET", "/api/cards/"+study["id"].(string), a, w.ID, nil, &card)
	if len(card["teams"].(map[string]any)) != 4 {
		t.Fatalf("study teams: %v", card["teams"])
	}
	// The card's own version counts its edits; only its Teams are checked.
	for name, body := range map[string]any{"create": created, "list": list, "detail": detail, "study teams": card["teams"]} {
		if keys := versionKeys(body, ""); len(keys) > 0 {
			t.Errorf("%s returns %v", name, keys)
		}
	}
	for name, body := range map[string]any{"create": created, "list": list, "detail": detail} {
		if hasKey(body, "blob_key") {
			t.Errorf("%s returns blob_key", name)
		}
	}
	if c := s.call(t, "GET", "/api/packs?name=formteam", a, w.ID, nil, nil); c != 404 {
		t.Errorf("version list: %d", c)
	}
}

func TestDuplicateThisTeam(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	src := s.builtInID(t, w.ID, "grounding")
	var made struct{ ID, Name string }
	if c := s.call(t, "POST", "/api/teams/"+src+"/duplicate", a, w.ID, map[string]string{"name": "My grounding"}, &made); c != 201 {
		t.Fatalf("duplicate: %d", c)
	}
	if made.ID == src || made.Name != "My grounding" {
		t.Fatalf("copy %+v", made)
	}
	var source, copied struct{ Manifest pack.Manifest }
	s.call(t, "GET", "/api/teams/"+src, a, w.ID, nil, &source)
	s.call(t, "GET", "/api/teams/"+made.ID, a, w.ID, nil, &copied)
	source.Manifest.Name = copied.Manifest.Name
	if !reflect.DeepEqual(source.Manifest, copied.Manifest) {
		t.Fatalf("contents differ:\n%+v\n%+v", source.Manifest, copied.Manifest)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack WHERE id = $1 AND source = 'uploaded' AND stage = 'grounding' AND uploaded_by IS NOT NULL", made.ID); n != 1 {
		t.Fatal("the copy is not an uploaded grounding Team")
	}
	if n := s.count(t, "SELECT count(*) FROM team_agent WHERE pack_id = $1", made.ID); n != 3 {
		t.Fatalf("copy added with %d agents", n)
	}
	if c, e := s.saveSkills(t, a, w.ID, made.ID, "aeon"); c != 200 {
		t.Fatalf("edit the copy: %d %q", c, e)
	}
	for _, name := range []string{"My grounding", "Hypotheses Team"} {
		if c := s.call(t, "POST", "/api/teams/"+src+"/duplicate", a, w.ID, map[string]string{"name": name}, nil); c != 409 {
			t.Fatalf("taken name %q: %d", name, c)
		}
	}
	if c := s.call(t, "POST", "/api/teams/"+src+"/duplicate", a, w.ID, map[string]string{"name": " "}, nil); c != 400 {
		t.Fatalf("blank name: %d", c)
	}
}

func TestTheStoreShowsWhetherIAddedIt(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "mine"}), nil)
	got := s.storeList(t, a, w.ID, "")
	if len(got) != 1 || got[0].Added {
		t.Fatalf("store %+v", got)
	}
	if c := s.call(t, "POST", "/api/teams/"+got[0].ID+"/add", a, w.ID, nil, nil); c != 200 {
		t.Fatalf("add: %d", c)
	}
	if got = s.storeList(t, a, w.ID, ""); !got[0].Added {
		t.Fatalf("after adding: %+v", got)
	}
}

func TestAColleaguesUploadIsNotInMyStore(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "ana@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	var up struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "ana-only"}), &up)
	if got := s.storeList(t, b, w.ID, ""); len(got) != 0 {
		t.Fatalf("Ben sees %+v", got)
	}
	if c := s.call(t, "GET", "/api/teams/"+up.ID, b, w.ID, nil, nil); c != 404 {
		t.Fatalf("Ben reads Ana's Team: %d", c)
	}
	if c := s.call(t, "POST", "/api/teams/"+up.ID+"/add", b, w.ID, nil, nil); c != 404 {
		t.Fatalf("Ben adds Ana's Team: %d", c)
	}
	if got := s.storeList(t, a, w.ID, ""); len(got) != 1 {
		t.Fatalf("Ana sees %+v", got)
	}
}

func TestFormTeamSnapshotsTheAgents(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	lead, aud, mem := s.newAgent(t, a, w.ID, "lead", "claude"), s.newAgent(t, a, w.ID, "aud", "codex"), s.newAgent(t, a, w.ID, "mem", "claude")
	var made struct{ ID string }
	if c := s.call(t, "POST", "/api/teams", a, w.ID, formFor(lead, aud, mem), &made); c != 201 {
		t.Fatalf("create: %d", c)
	}
	if c := s.call(t, "PATCH", "/api/agents/"+mem, a, w.ID, map[string]string{"instructions": "Changed", "harness": "codex"}, nil); c != 200 {
		t.Fatalf("edit: %d", c)
	}
	var team struct {
		Manifest struct {
			Members []struct {
				Agent struct{ Harness, Instructions string }
			}
		}
	}
	if c := s.call(t, "GET", "/api/teams/"+made.ID, a, w.ID, nil, &team); c != 200 {
		t.Fatalf("get: %d", c)
	}
	got := team.Manifest.Members[0].Agent
	if got.Harness != "claude" || got.Instructions != "Be mem" {
		t.Fatalf("snapshot moved with the agent: %+v", got)
	}
	// The creator has already added their Team: three private agents beside the three they made.
	if n := s.count(t, "SELECT count(*) FROM team_agent WHERE pack_id = $1", made.ID); n != 3 {
		t.Fatalf("%d team agents", n)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type IN ('team.created', 'team.added') AND payload - 'pack_id' = '{}'::jsonb"); n != 2 {
		t.Fatalf("%d bare team events", n)
	}
}

func TestAnAgentICannotSeeCannotBeAMember(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "ana@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	var secret struct{ ID string }
	if c := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"name": "secret", "harness": "claude", "visibility": "private"}, &secret); c != 201 {
		t.Fatalf("agent: %d", c)
	}
	aud, mem := s.newAgent(t, b, w.ID, "aud", "codex"), s.newAgent(t, b, w.ID, "mem", "claude")
	if c := s.call(t, "POST", "/api/teams", b, w.ID, formFor(secret.ID, aud, mem), nil); c != 400 {
		t.Fatalf("a private agent of Ana's as Ben's leader: %d", c)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatalf("%d packs stored", n)
	}
}

func TestAFormTeamWithTheAuditorOnTheLeadersModelIsRefused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	lead, mem := s.newAgent(t, a, w.ID, "lead", "claude"), s.newAgent(t, a, w.ID, "mem", "codex")
	aud := s.newAgent(t, a, w.ID, "aud", "claude")
	var e json.RawMessage
	if c := s.call(t, "POST", "/api/teams", a, w.ID, formFor(lead, aud, mem), &e); c != 400 {
		t.Fatalf("create: %d %s", c, e)
	}
}

func TestReadingTheStoreWritesNothing(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "bio"}))
	if got := s.storeList(t, a, w.ID, ""); len(got) != 0 {
		t.Fatalf("store %+v", got)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatalf("a read stored %d packs", n)
	}
}

func TestAFormWithARepeatedSlotNameIsRefused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	lead, aud, mem := s.newAgent(t, a, w.ID, "lead", "claude"), s.newAgent(t, a, w.ID, "aud", "codex"), s.newAgent(t, a, w.ID, "mem", "claude")
	f := formFor(lead, aud, mem)
	f["members"].([]any)[0].(map[string]any)["name"] = "pi"
	var e struct{ Error string }
	if c := s.call(t, "POST", "/api/teams", a, w.ID, f, &e); c != 400 || !strings.Contains(e.Error, `"pi"`) {
		t.Fatalf("create: %d %q", c, e.Error)
	}
}

func TestNewWorkspaceGetsBuiltInTeams(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "Grounding Team", Stage: "grounding"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	got := s.storeList(t, a, w.ID, "")
	if len(got) != 1 || got[0].Name != "Grounding Team" || !got[0].Added {
		t.Fatalf("store %+v", got)
	}
}

func TestStudyWithABuiltInTeamCreatesTheOwnersAgents(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = shippedFS(t, testutil.Pack(t, testutil.PackOpts{Name: "Grounding Team", Stage: "grounding"}))
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "ben@lab.org", "member")
	pack := s.storeList(t, b, w.ID, "")[0].ID
	var c studyJSON
	if code := s.call(t, "POST", "/api/cards", b, w.ID, map[string]any{"question": "q",
		"assignee": teamAssignee(map[string]string{"grounding": pack})}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	ben := uuid.MustParse(s.userID(t, b))
	var n int
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(w.ID), ben, func(tx *store.Tx) error {
		rows, err := tx.Q.TeamAgentsFor(t.Context(), db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: uuid.MustParse(pack), UserID: ben})
		n = len(rows)
		return err
	})
	if err != nil || n != 3 {
		t.Fatalf("team agents %d, %v", n, err)
	}
}

func TestATeamCannotWorkReporting(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	lead, aud, mem := s.newAgent(t, a, w.ID, "lead", "claude"), s.newAgent(t, a, w.ID, "aud", "codex"), s.newAgent(t, a, w.ID, "mem", "claude")
	form := formFor(lead, aud, mem)
	form["stage"] = "reporting"
	var e struct{ Error string }
	if c := s.call(t, "POST", "/api/teams", a, w.ID, form, &e); c != 400 || !strings.Contains(e.Error, `stage "reporting" is not a stage`) {
		t.Fatalf("a Team on reporting: %d %q", c, e.Error)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack WHERE stage = 'reporting'"); n != 0 {
		t.Fatalf("%d reporting Teams", n)
	}
	form["stage"] = "validation"
	if c := s.call(t, "POST", "/api/teams", a, w.ID, form, nil); c != 201 {
		t.Fatalf("the same form on validation: %d", c)
	}
}
