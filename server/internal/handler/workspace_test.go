package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"regexp"
	"sync"
	"testing"
)

type ws struct {
	ID, Slug, Name, Role string
}

func (s *server) workspace(t *testing.T, tok, slug string) ws {
	t.Helper()
	var w ws
	if c := s.call(t, "POST", "/api/workspaces", tok, "", map[string]string{"name": "Lab " + slug, "slug": slug}, &w); c != 201 {
		t.Fatalf("create workspace: %d", c)
	}
	return w
}

// invite returns the token from the invitation email.
func (s *server) invite(t *testing.T, tok, wsID, email, role string) string {
	t.Helper()
	if c := s.call(t, "POST", "/api/workspaces/"+wsID+"/invitations", tok, "", map[string]string{"email": email, "role": role}, nil); c != 201 {
		t.Fatalf("invite: %d", c)
	}
	return regexp.MustCompile(`invitations/([A-Za-z0-9_-]+)/accept`).FindStringSubmatch(s.Mail.last[email])[1]
}

func (s *server) userID(t *testing.T, tok string) string {
	var me struct{ ID string }
	s.call(t, "GET", "/api/me", tok, "", nil, &me)
	return me.ID
}

func TestCreateAndList(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "smith-lab")
	if c := s.call(t, "POST", "/api/workspaces", a, "", map[string]string{"name": "x", "slug": "smith-lab"}, nil); c != 409 {
		t.Fatalf("duplicate slug: %d", c)
	}
	if c := s.call(t, "POST", "/api/workspaces", a, "", map[string]string{"name": "x", "slug": "Bad Slug"}, nil); c != 400 {
		t.Fatalf("bad slug: %d", c)
	}
	var list []ws
	s.call(t, "GET", "/api/workspaces", a, "", nil, &list)
	if len(list) != 1 || list[0].ID != w.ID || list[0].Role != "owner" {
		t.Fatalf("%+v", list)
	}
}

func TestInvitationFlow(t *testing.T) {
	s := newServer(t)
	a, b, c := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org"), s.signIn(t, "c@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.invite(t, a, w.ID, "b@lab.org", "member")
	if code := s.call(t, "POST", "/api/invitations/"+tok+"/accept", c, "", nil, nil); code != 404 {
		t.Fatalf("someone else accepted b's invitation: %d", code)
	}
	if code := s.call(t, "POST", "/api/invitations/"+tok+"/accept", b, "", nil, nil); code != 200 {
		t.Fatalf("accept: %d", code)
	}
	if code := s.call(t, "POST", "/api/invitations/"+tok+"/accept", b, "", nil, nil); code != 404 {
		t.Fatal("an invitation was accepted twice")
	}
	var members []map[string]any
	s.call(t, "GET", "/api/workspaces/"+w.ID+"/members", b, "", nil, &members)
	if len(members) != 2 {
		t.Fatalf("%+v", members)
	}
	if code := s.call(t, "POST", "/api/workspaces/"+w.ID+"/invitations", b, "", map[string]string{"email": "d@x", "role": "member"}, nil); code != 403 {
		t.Fatalf("a plain member invited someone: %d", code)
	}
}

func TestLastOwnerIsProtected(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "admin")+"/accept", b, "", nil, nil)
	aID := s.userID(t, a)
	if c := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/members/"+aID, b, "", map[string]string{"role": "member"}, nil); c != 409 {
		t.Fatalf("demoted the last owner: %d", c)
	}
	if c := s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/members/"+aID, b, "", nil, nil); c != 409 {
		t.Fatalf("removed the last owner: %d", c)
	}
	if c := s.call(t, "POST", "/api/workspaces/"+w.ID+"/leave", a, "", nil, nil); c != 409 {
		t.Fatalf("the last owner left: %d", c)
	}
	if c := s.call(t, "DELETE", "/api/workspaces/"+w.ID, b, "", nil, nil); c != 403 {
		t.Fatalf("an admin deleted the workspace: %d", c)
	}
	if c := s.call(t, "POST", "/api/workspaces/"+w.ID+"/leave", b, "", nil, nil); c != 204 {
		t.Fatalf("an admin could not leave: %d", c)
	}
}

func TestStrangersGet404(t *testing.T) {
	s := newServer(t)
	a, x := s.signIn(t, "a@lab.org"), s.signIn(t, "x@else.org")
	w := s.workspace(t, a, "lab")
	for _, p := range []string{"", "/members", "/invitations"} {
		if c := s.call(t, "GET", "/api/workspaces/"+w.ID+p, x, "", nil, nil); c != 404 {
			t.Errorf("GET %s: %d", p, c)
		}
	}
}

func raceCalls(t *testing.T, calls ...func() int) []int {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	out := make([]int, len(calls))
	for i, call := range calls {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; out[i] = call() }()
	}
	close(start)
	wg.Wait()
	return out
}

func TestConcurrentLastOwnerMutations(t *testing.T) {
	for _, kind := range []string{"demote", "remove", "leave"} {
		t.Run(kind, func(t *testing.T) {
			s := newServer(t)
			a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
			w := s.workspace(t, a, "race-lab")
			s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "admin")+"/accept", b, "", nil, nil)
			ids := []string{s.userID(t, a), s.userID(t, b)}
			if c := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/members/"+ids[1], a, "", map[string]string{"role": "owner"}, nil); c != 204 {
				t.Fatal(c)
			}
			calls := make([]func() int, 2)
			for i, tok := range []string{a, b} {
				calls[i] = func() int {
					path := "/api/workspaces/" + w.ID + "/members/" + ids[i]
					if kind == "demote" {
						return s.call(t, "PATCH", path, tok, "", map[string]string{"role": "member"}, nil)
					}
					if kind == "leave" {
						return s.call(t, "POST", "/api/workspaces/"+w.ID+"/leave", tok, "", nil, nil)
					}
					return s.call(t, "DELETE", path, tok, "", nil, nil)
				}
			}
			codes := raceCalls(t, calls...)
			success, refused := 0, 0
			for _, c := range codes {
				if c == 204 {
					success++
				} else if c == 409 {
					refused++
				} else {
					t.Fatalf("statuses %v", codes)
				}
			}
			var n int
			if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM member WHERE workspace_id=$1 AND role='owner'", w.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if success != 1 || refused != 1 || n != 1 {
				t.Fatalf("codes=%v owners=%d", codes, n)
			}
		})
	}
}

func TestConcurrentInvitationSingleUseAndExistingRole(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "race-lab")
	token := s.invite(t, a, w.ID, "b@lab.org", "member")
	calls := make([]func() int, 12)
	for i := range calls {
		calls[i] = func() int { return s.call(t, "POST", "/api/invitations/"+token+"/accept", b, "", nil, nil) }
	}
	success := 0
	for _, c := range raceCalls(t, calls...) {
		if c == 200 {
			success++
		} else if c != 404 {
			t.Fatal(c)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	token = s.invite(t, a, w.ID, "b@lab.org", "admin")
	var out struct{ Role string }
	if c := s.call(t, "POST", "/api/invitations/"+token+"/accept", b, "", nil, &out); c != 200 || out.Role != "member" {
		t.Fatalf("%d %+v", c, out)
	}
	var joined int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='member.joined'", w.ID).Scan(&joined); err != nil {
		t.Fatal(err)
	}
	if joined != 1 {
		t.Fatal(joined)
	}
}

func TestWorkspaceEventsMissingIDsAndDeletion(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "event-lab")
	var empty json.RawMessage
	if c := s.call(t, "GET", "/api/workspaces/"+w.ID+"/invitations", a, "", nil, &empty); c != 200 || string(empty) != "[]" {
		t.Fatalf("%d %s", c, empty)
	}
	token := s.invite(t, a, w.ID, "b@lab.org", "member")
	s.call(t, "POST", "/api/invitations/"+token+"/accept", b, "", nil, nil)
	bid := s.userID(t, b)
	for _, c := range []int{
		s.call(t, "PATCH", "/api/workspaces/"+w.ID, a, "", map[string]string{"name": "Renamed"}, nil),
		s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/members/"+bid, a, "", map[string]string{"role": "admin"}, nil),
		s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/members/"+bid, a, "", nil, nil),
	} {
		if c != 200 && c != 204 {
			t.Fatal(c)
		}
	}
	var inv struct{ ID string }
	s.call(t, "POST", "/api/workspaces/"+w.ID+"/invitations", a, "", map[string]string{"email": "c@lab.org", "role": "member"}, &inv)
	if c := s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/invitations/"+inv.ID, a, "", nil, nil); c != 204 {
		t.Fatal(c)
	}
	for _, path := range []string{"/members/", "/invitations/"} {
		if c := s.call(t, "DELETE", "/api/workspaces/"+w.ID+path+uuid.NewString(), a, "", nil, nil); c != 404 {
			t.Fatal(c)
		}
	}
	rows, err := s.Env.Owner.Query(t.Context(), "SELECT DISTINCT type FROM event WHERE workspace_id=$1", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatal(err)
		}
		types[typ] = true
	}
	rows.Close()
	for _, typ := range []string{"workspace.created", "workspace.updated", "member.joined", "member.role_changed", "member.removed", "invitation.created", "invitation.accepted", "invitation.revoked"} {
		if !types[typ] {
			t.Fatal(typ)
		}
	}
	var published []string
	s.H.Store.Publish = func(_ context.Context, events []db.Event) {
		for _, e := range events {
			published = append(published, e.Type)
		}
	}
	if c := s.call(t, "DELETE", "/api/workspaces/"+w.ID, a, "", nil, nil); c != 204 {
		t.Fatal(c)
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM event WHERE workspace_id=$1", w.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(published) != 1 || published[0] != "workspace.deleted" {
		t.Fatalf("events=%d published=%v", n, published)
	}
	if c := s.call(t, "GET", "/api/workspaces/"+w.ID, a, "", nil, nil); c != 404 {
		t.Fatal(c)
	}
}

func TestInvitationRollbackExpiryAndCrossWorkspaceRevoke(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "rollback-lab")
	other := s.workspace(t, a, "other-lab")
	var inv struct{ ID, Token string }
	if c := s.call(t, "POST", "/api/workspaces/"+w.ID+"/invitations", a, "", map[string]string{"email": "b@lab.org", "role": "member"}, &inv); c != 201 {
		t.Fatal(c)
	}
	if c := s.call(t, "DELETE", "/api/workspaces/"+other.ID+"/invitations/"+inv.ID, a, "", nil, nil); c != 404 {
		t.Fatal(c)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "ALTER TABLE event ADD CONSTRAINT reject_join CHECK (type <> 'member.joined')"); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "POST", "/api/invitations/"+inv.Token+"/accept", b, "", nil, nil); c != 500 {
		t.Fatal(c)
	}
	var accepted bool
	var members, events int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT accepted_at IS NOT NULL FROM workspace_invitation WHERE id=$1", inv.ID).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM member WHERE workspace_id=$1", w.ID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='invitation.accepted'", w.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if accepted || members != 1 || events != 0 {
		t.Fatalf("accepted=%v members=%d events=%d", accepted, members, events)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "ALTER TABLE event DROP CONSTRAINT reject_join"); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "POST", "/api/invitations/"+inv.Token+"/accept", b, "", nil, nil); c != 200 {
		t.Fatal(c)
	}
	inv.ID = ""
	inv.Token = ""
	if c := s.call(t, "POST", "/api/workspaces/"+w.ID+"/invitations", a, "", map[string]string{"email": "b@lab.org", "role": "member"}, &inv); c != 201 {
		t.Fatal(c)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE workspace_invitation SET expires_at=now()-interval '1 second' WHERE id=$1", inv.ID); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "POST", "/api/invitations/"+inv.Token+"/accept", b, "", nil, nil); c != 404 {
		t.Fatal(c)
	}
}

func TestALeavingMemberTakesTheirSharingWithThem(t *testing.T) {
	for _, leave := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "leave"}[leave], func(t *testing.T) {
			f, guest, guestID := machineStudy(t)
			delivered := 0
			f.s.H.AgentRuns.OnSettlement = func(ctx context.Context, event service.Settlement) error {
				delivered++
				if n := f.s.count(t, "SELECT count(*) FROM member WHERE workspace_id=$1 AND user_id=$2", f.ws, guestID); n != 0 {
					t.Error("settled before membership removal committed")
				}
				if n := f.s.count(t, "SELECT count(*) FROM runtime WHERE workspace_id=$1 AND owner_id=$2 AND visibility<>'private'", f.ws, guestID); n != 0 {
					t.Error("settled before sharing cleanup committed")
				}
				return nil
			}
			var runtimes []db.Runtime
			if code := f.s.call(t, "POST", "/api/daemon/register", f.s.personalToken(t, guest), "", map[string]any{"workspace_id": f.ws, "daemon_id": "guest-daemon", "device_name": "guest", "harnesses": []string{"claude"}}, &runtimes); code != 200 {
				t.Fatal(code)
			}
			gr := runtimes[0].ID.String()
			ownerID := f.s.userID(t, f.jwt)
			for _, rt := range []string{f.rt, gr} {
				if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET visibility='workspace' WHERE id=$1", rt); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.s.Env.Owner.Exec(t.Context(), "INSERT INTO runtime_share(workspace_id,runtime_id,user_id) VALUES($1,$2,$3),($1,$4,$5)", f.ws, f.rt, guestID, gr, ownerID); err != nil {
				t.Fatal(err)
			}
			var runs []string
			for i, tc := range []struct{ tok, machine, rt, status string }{{guest, "skills-daemon", f.rt, "running"}, {guest, "skills-daemon", "", "queued"}, {f.jwt, "guest-daemon", gr, "dispatched"}, {f.jwt, "guest-daemon", "", "queued"}, {guest, "", "", "queued"}, {f.jwt, "", "", "queued"}} {
				var c card
				in := map[string]any{"question": "departure " + uuid.NewString()}
				if tc.machine != "" {
					in["machine"] = tc.machine
				}
				if code := f.s.call(t, "POST", "/api/cards", tc.tok, f.ws, in, &c); code != 201 {
					t.Fatal(code)
				}
				id := uuid.NewString()
				runs = append(runs, id)
				user := ownerID
				if tc.tok == guest {
					user = guestID
				}
				if _, err := f.s.Env.Owner.Exec(t.Context(), "INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,runtime_id) VALUES($1,$2,$3,$4,'grounding',1,'leader','claude','q',$5,NULLIF($6,'')::uuid)", id, f.ws, c.ID, user, tc.status, tc.rt); err != nil {
					t.Fatalf("run %d: %v", i, err)
				}
			}
			path, method, tok := "/api/workspaces/"+f.ws+"/members/"+guestID, "DELETE", f.jwt
			if leave {
				path, method, tok = "/api/workspaces/"+f.ws+"/leave", "POST", guest
			}
			if code := f.s.call(t, method, path, tok, "", nil, nil); code != 204 {
				t.Fatal(code)
			}
			if delivered != 4 {
				t.Errorf("settlements delivered: %d", delivered)
			}
			if n := f.s.count(t, "SELECT count(*) FROM runtime_share WHERE workspace_id=$1", f.ws); n != 0 {
				t.Errorf("shares left: %d", n)
			}
			if n := f.s.count(t, "SELECT count(*) FROM runtime WHERE id=$1 AND visibility='private'", gr); n != 1 {
				t.Error("departed runtime still shared")
			}
			for i, id := range runs {
				want := "queued"
				if i < 4 {
					want = "cancelled"
				}
				if n := f.s.count(t, "SELECT count(*) FROM agent_run WHERE id=$1 AND status=$2", id, want); n != 1 {
					t.Errorf("run %d not %s", i, want)
				}
				if i < 4 {
					if n := f.s.count(t, "SELECT count(*) FROM agent_run WHERE id=$1 AND cancel_reason='runtime_access_revoked'", id); n != 1 {
						t.Errorf("run %d missing reason", i)
					}
					if n := f.s.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1", id); n != 1 {
						t.Errorf("run %d missing settlement", i)
					}
				}
			}
		})
	}
}

func TestALeavingMembersLateRunCannotClaimWorkspaceSharing(t *testing.T) {
	f, guest, guestID := machineStudy(t)
	if code := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", f.jwt, f.ws, map[string]any{"visibility": "workspace"}, nil); code != 200 {
		t.Fatal(code)
	}
	var c card
	if code := f.s.call(t, "POST", "/api/cards", guest, f.ws, map[string]any{"question": "late run", "machine": "skills-daemon"}, &c); code != 201 {
		t.Fatal(code)
	}
	if code := f.s.call(t, "POST", "/api/workspaces/"+f.ws+"/leave", guest, "", nil, nil); code != 204 {
		t.Fatal(code)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), "INSERT INTO agent_run(workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt) VALUES($1,$2,$3,'grounding',1,'leader','claude','late work')", f.ws, c.ID, guestID); err != nil {
		t.Fatal(err)
	}
	ws, rt := uuid.MustParse(f.ws), uuid.MustParse(f.rt)
	err := f.s.H.Store.InWorkspace(t.Context(), ws, uuid.MustParse(f.s.userID(t, f.jwt)), func(tx *store.Tx) error {
		_, err := tx.Q.ClaimAgentRun(t.Context(), db.ClaimAgentRunParams{WorkspaceID: ws, RuntimeID: &rt})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("departed member's late run claim: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
