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
	csUpload(t, s, a, ws, p.ID, "x.txt", 0, false, []byte("x"))
	if code := s.call(t, "POST", base+"/archive", a, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{{"PATCH", base, map[string]string{"name": "changed"}}, {"POST", base + "/archive", nil}, {"POST", base + "/members", map[string]string{"user_id": s.userID(t, b), "role": "member"}}, {"DELETE", base + "/members/" + s.userID(t, a), nil}} {
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

func TestArchiveRefusesEveryActiveProjectWork(t *testing.T) {
	for _, kind := range []string{"cell", "setup", "run", "turn"} {
		for _, status := range []string{"queued", "running"} {
			t.Run(kind+status, func(t *testing.T) {
				s, a, _, _, ws, p := csFixture(t)
				path, chat := csChat(t, s, a, ws, p.ID)
				var code int
				switch kind {
				case "cell":
					code = s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil)
				case "setup":
					code = s.call(t, "POST", csProjects+"/"+p.ID+"/environment", a, ws, map[string]any{"chat_id": chat.ID, "python": "3.13"}, nil)
				case "run":
					csUpload(t, s, a, ws, p.ID, "analysis.py", 0, false, []byte("print(1)"))
					code = s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": chat.ID, "script": "analysis.py"}, nil)
				case "turn":
					code = s.call(t, "POST", path+"/turns", a, ws, map[string]string{"body": "hello"}, nil)
				}
				if code != 201 {
					t.Fatalf("enqueue %d", code)
				}
				table, column := "coresearch_"+kind, "chat_id"
				if kind == "turn" {
					table, column = "agent_run", "coresearch_chat_id"
				}
				if status == "running" {
					if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE "+table+" SET status=$1 WHERE "+column+"=$2", status, chat.ID); err != nil {
						t.Fatal(err)
					}
				}
				if code = s.call(t, "POST", csProjects+"/"+p.ID+"/archive", a, ws, nil, nil); code != 409 {
					t.Fatalf("archive accepted active %s: %d", kind, code)
				}
			})
		}
	}
}
func TestWorkspaceDeparturePreservesProjectOwnership(t *testing.T) {
	for _, admin := range []bool{false, true} {
		t.Run(map[bool]string{false: "leave", true: "admin"}[admin], func(t *testing.T) {
			s, a, b, c, ws, _ := csFixture(t)
			if _, err := s.Env.Owner.Exec(t.Context(), "INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')", ws, s.userID(t, c)); err != nil {
				t.Fatal(err)
			}
			var p csProject
			if code := s.call(t, "POST", csProjects, b, ws, map[string]string{"name": "Private"}, &p); code != 201 {
				t.Fatal(code)
			}
			if code := csAdd(t, s, b, c, ws, p.ID, "member"); code != 201 {
				t.Fatal(code)
			}
			path, tok := "/api/workspaces/"+ws+"/leave", b
			method, want := "POST", 409
			if admin {
				path, tok, method, want = "/api/workspaces/"+ws+"/members/"+s.userID(t, b), a, "DELETE", 204
			}
			if code := s.call(t, method, path, tok, "", nil, nil); code != want {
				t.Fatalf("departure %d want %d", code, want)
			}
			if !admin {
				var reply map[string]string
				if code := s.call(t, method, path, tok, "", nil, &reply); code != 409 || reply["error"] != errProjectOwnerDeparture.Error() {
					t.Fatal(code, reply)
				}
				if code := csAdd(t, s, b, c, ws, p.ID, "owner"); code != 201 {
					t.Fatal(code)
				}
				if code := s.call(t, method, path, tok, "", nil, nil); code != 204 {
					t.Fatal("leave after transfer", code)
				}
			}
			if admin {
				var role string
				if err := s.Env.Owner.QueryRow(t.Context(), "SELECT role FROM coresearch_member WHERE project_id=$1 AND user_id=$2", p.ID, s.userID(t, c)).Scan(&role); err != nil || role != "owner" {
					t.Fatalf("successor %q %v", role, err)
				}
				if code := s.call(t, "GET", csProjects+"/"+p.ID, a, ws, nil, nil); code != 404 {
					t.Fatalf("admin gained private access %d", code)
				}
				if code := s.call(t, "PATCH", csProjects+"/"+p.ID, c, ws, map[string]string{"name": "Recovered"}, nil); code != 200 {
					t.Fatal(code)
				}
			}
		})
	}
}
func TestEmptyProjectArchivesOnWorkspaceDeparture(t *testing.T) {
	s, _, b, _, ws, _ := csFixture(t)
	var p csProject
	if code := s.call(t, "POST", csProjects, b, ws, map[string]string{"name": "Solo"}, &p); code != 201 {
		t.Fatal(code)
	}
	csUpload(t, s, b, ws, p.ID, "keep.txt", 0, false, []byte("retain"))
	if code := s.call(t, "POST", "/api/workspaces/"+ws+"/leave", b, "", nil, nil); code != 204 {
		t.Fatal(code)
	}
	var archived bool
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT archived_at IS NOT NULL FROM coresearch_project WHERE id=$1", p.ID).Scan(&archived); err != nil || !archived {
		t.Fatalf("archive %v %v", archived, err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM coresearch_file WHERE project_id=$1", p.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("files %d %v", n, err)
	}
}

func TestAdministrativeDepartureChoosesOnlyRequiredExistingSuccessor(t *testing.T) {
	for _, mode := range []string{"earliest", "tie", "existing-owner"} {
		t.Run(mode, func(t *testing.T) {
			s, a, b, c, ws, _ := csFixture(t)
			d := s.signIn(t, "d@lab.org")
			for _, token := range []string{c, d} {
				if _, e := s.Env.Owner.Exec(t.Context(), "INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')", ws, s.userID(t, token)); e != nil {
					t.Fatal(e)
				}
			}
			var p csProject
			if code := s.call(t, "POST", csProjects, b, ws, map[string]string{"name": "Private"}, &p); code != 201 {
				t.Fatal(code)
			}
			for _, token := range []string{c, d} {
				role := "member"
				if mode == "existing-owner" && token == c {
					role = "owner"
				}
				if code := csAdd(t, s, b, token, ws, p.ID, role); code != 201 {
					t.Fatal(code)
				}
			}
			cid, did := s.userID(t, c), s.userID(t, d)
			if _, e := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_member SET created_at='2020-01-01' WHERE project_id=$1 AND user_id<>$2", p.ID, s.userID(t, b)); e != nil {
				t.Fatal(e)
			}
			if mode == "earliest" {
				if _, e := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_member SET created_at='2021-01-01' WHERE project_id=$1 AND user_id=$2", p.ID, did); e != nil {
					t.Fatal(e)
				}
			}
			expected := cid
			if mode == "tie" && did < cid {
				expected = did
			}
			if code := s.call(t, "DELETE", "/api/workspaces/"+ws+"/members/"+s.userID(t, b), a, "", nil, nil); code != 204 {
				t.Fatal(code)
			}
			var owners []string
			rows, e := s.Env.Owner.Query(t.Context(), "SELECT user_id::text FROM coresearch_member WHERE project_id=$1 AND role='owner'", p.ID)
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					t.Fatal(e)
				}
				owners = append(owners, id)
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			if len(owners) != 1 || owners[0] != expected {
				t.Fatalf("owners %v expected %s", owners, expected)
			}
		})
	}
}
