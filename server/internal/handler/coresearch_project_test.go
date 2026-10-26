package handler

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const csProjects = "/api/coresearch/projects"

type csProject struct{ ID, Name, Role string }

func csFixture(t *testing.T) (*server, string, string, string, string, csProject) {
	t.Helper()
	s := newServer(t)
	a, b, c := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org"), s.signIn(t, "c@other.org")
	ws := s.workspace(t, a, "coresearch").ID
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES ($1,$2,'member')`, ws, s.userID(t, b)); e != nil {
		t.Fatal(e)
	}
	var p csProject
	if code := s.call(t, "POST", csProjects, a, ws, map[string]string{"name": "Experiment"}, &p); code != 201 || p.ID == "" {
		t.Fatalf("create %d %+v", code, p)
	}
	return s, a, b, c, ws, p
}
func csAdd(t *testing.T, s *server, a, b, ws, p, role string) int {
	t.Helper()
	return s.call(t, "POST", csProjects+"/"+p+"/members", a, ws, map[string]string{"user_id": s.userID(t, b), "role": role}, nil)
}
func TestProjectsAreMembersOnly(t *testing.T) {
	s, a, b, c, ws, p := csFixture(t)
	path := csProjects + "/" + p.ID
	for _, tok := range []string{b, c} {
		for _, suffix := range []string{"", "/members", "/files"} {
			if code := s.call(t, "GET", path+suffix, tok, ws, nil, nil); code != 404 {
				t.Fatalf("nonmember GET %s: %d", suffix, code)
			}
		}
	}
	var list []csProject
	if code := s.call(t, "GET", csProjects, b, ws, nil, &list); code != 200 || len(list) != 0 {
		t.Fatalf("hidden list %d %+v", code, list)
	}
	if code := csAdd(t, s, a, b, ws, p.ID, "member"); code != 201 {
		t.Fatal(code)
	}
	if code := s.call(t, "GET", path, b, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	if code := csAdd(t, s, a, c, ws, p.ID, "member"); code != 400 {
		t.Fatalf("workspace outsider %d", code)
	}
}
func TestOnlyOwnersManageMembers(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	if code := csAdd(t, s, a, b, ws, p.ID, "member"); code != 201 {
		t.Fatal(code)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{{"POST", csProjects + "/" + p.ID + "/members", map[string]string{"user_id": s.userID(t, a), "role": "owner"}}, {"DELETE", csProjects + "/" + p.ID + "/members/" + s.userID(t, a), nil}, {"PATCH", csProjects + "/" + p.ID, map[string]string{"name": "changed"}}, {"POST", csProjects + "/" + p.ID + "/archive", nil}} {
		if code := s.call(t, r.method, r.path, b, ws, r.body, nil); code != 403 {
			t.Fatalf("%s: %d", r.method, code)
		}
	}
}
func TestLastOwnerStays(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	path := csProjects + "/" + p.ID + "/members/" + s.userID(t, a)
	if code := s.call(t, "DELETE", path, a, ws, nil, nil); code != 409 {
		t.Fatal(code)
	}
	if code := csAdd(t, s, a, a, ws, p.ID, "member"); code != 409 {
		t.Fatalf("demoted last owner: %d", code)
	}
	if code := csAdd(t, s, a, b, ws, p.ID, "owner"); code != 201 {
		t.Fatal(code)
	}
	if code := s.call(t, "DELETE", path, b, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
}
func TestArchivedProjectRefusesWrites(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	base := csProjects + "/" + p.ID
	_, v := csUpload(t, s, a, ws, p.ID, "x.txt", 0, false, []byte("x"))
	if code := s.call(t, "POST", base+"/archive", a, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{{"PATCH", base, map[string]string{"name": "changed"}}, {"POST", base + "/archive", nil}, {"POST", base + "/members", map[string]string{"user_id": s.userID(t, b), "role": "member"}}, {"DELETE", base + "/members/" + s.userID(t, a), nil}, {"POST", base + "/files/" + v.FileID.String() + "/restore", map[string]int{"n": 1, "base": 1}}} {
		if code := s.call(t, r.method, r.path, a, ws, r.body, nil); code != 409 {
			t.Fatalf("%s: %d", r.path, code)
		}
	}
	if code, _ := csUpload(t, s, a, ws, p.ID, "x.txt", 1, false, []byte("y")); code != 409 {
		t.Fatal(code)
	}
	if code := s.call(t, "GET", base+"/files", a, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
}
func TestCoresearchCreationHelperAuthority(t *testing.T) {
	s, a, _, c, ws, _ := csFixture(t)
	for _, function := range []string{"public.coresearch_create_project('spoof')", "public.coresearch_blob_referenced('sha')"} {
		for _, tc := range []struct{ user, ws string }{{s.userID(t, c), ws}, {s.userID(t, a), uuid.NewString()}} {
			tx, e := s.Env.App.Begin(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(t.Context(), `SELECT set_config('app.user_id',$1,true),set_config('app.workspace_id',$2,true)`, tc.user, tc.ws); e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(t.Context(), `CREATE TEMP TABLE member AS SELECT $1::uuid AS workspace_id,$2::uuid AS user_id,'owner'::text AS role`, tc.ws, tc.user); e != nil {
				t.Fatal(e)
			}
			var value any
			e = tx.QueryRow(t.Context(), `SELECT `+function).Scan(&value)
			var pgerr *pgconn.PgError
			if !errors.As(e, &pgerr) || pgerr.Code != "42501" {
				t.Fatalf("helper authority %s: %v", function, e)
			}
			_ = tx.Rollback(t.Context())
		}
	}
}
