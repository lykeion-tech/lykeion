package handler

import (
	"github.com/google/uuid"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/service"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func machineStudy(t *testing.T) (runtimeSkillFixture, string, string) {
	t.Helper()
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	jwt := s.signIn(t, "machine-owner@lab.org")
	ws := s.workspace(t, jwt, "lab")
	token := s.personalToken(t, jwt)
	var runtimes []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", token, "", map[string]any{"workspace_id": ws.ID, "daemon_id": "skills-daemon", "device_name": "laptop", "platform": map[string]string{"os": "linux"}, "harnesses": []string{"claude"}}, &runtimes); code != 200 {
		t.Fatal(code)
	}
	f := runtimeSkillFixture{s, jwt, token, ws.ID, runtimes[0].ID}
	guest := f.s.signIn(t, "guest-machine@lab.org")
	invite := f.s.invite(t, f.jwt, f.ws, "guest-machine@lab.org", "member")
	if code := f.s.call(t, "POST", "/api/invitations/"+invite+"/accept", guest, "", nil, nil); code != 200 {
		t.Fatal(code)
	}
	return f, guest, f.s.userID(t, guest)
}

func TestAStudyIsCreatedOnASharedMachine(t *testing.T) {
	f, guest, _ := machineStudy(t)
	if code := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", f.jwt, f.ws, map[string]any{"visibility": "workspace"}, nil); code != 200 {
		t.Fatal(code)
	}
	for _, ref := range []string{"laptop", "skills-daemon"} {
		var c map[string]any
		if code := f.s.call(t, "POST", "/api/cards", guest, f.ws, map[string]any{"predicates": testPredicates, "question": "q-" + ref, "machine": ref}, &c); code != 201 {
			t.Fatalf("create %d %+v", code, c)
		}
		if c["machine"] != "skills-daemon" || c["machine_label"] != "laptop (machine-owner@lab.org)" {
			t.Fatalf("machine %+v", c)
		}
	}
}
func TestAStudyCantChooseAMachineNotSharedWithItsOwner(t *testing.T) {
	f, guest, _ := machineStudy(t)
	for _, ref := range []string{"laptop", "skills-daemon", "missing"} {
		var out map[string]any
		if code := f.s.call(t, "POST", "/api/cards", guest, f.ws, map[string]any{"predicates": testPredicates, "question": "q", "machine": ref}, &out); code != 400 || out["error"] != "No machine named "+ref+" is shared with you" {
			t.Fatalf("create %d %+v", code, out)
		}
	}
}
func TestOnlyTheOwnerChangesTheMachine(t *testing.T) {
	f, guest, _ := machineStudy(t)
	var c card
	if code := f.s.call(t, "POST", "/api/cards", f.jwt, f.ws, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 {
		t.Fatal(code)
	}
	var out map[string]any
	if code := f.s.call(t, "PATCH", "/api/cards/"+c.ID+"/machine", guest, f.ws, map[string]any{"machine": nil}, &out); code != 403 || out["error"] != "only the study's owner changes its machine" {
		t.Fatalf("patch %d %+v", code, out)
	}
}
func TestTheStudyPageExplainsAMissingMachine(t *testing.T) {
	f, _, _ := machineStudy(t)
	var c card
	if code := f.s.call(t, "POST", "/api/cards", f.jwt, f.ws, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 {
		t.Fatal(code)
	}
	for _, tc := range []struct{ machine, notice string }{{"gone", "This study's machine is no longer shared with you; choose another."}, {"skills-daemon", "This study's machine has no Codex shared with you; choose another."}} {
		if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE card SET machine=$1 WHERE id=$2", tc.machine, c.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE agent SET harness='codex' WHERE workspace_id=$1", f.ws); err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if code := f.s.call(t, "GET", "/api/cards/"+c.ID, f.jwt, f.ws, nil, &out); code != 200 {
			t.Fatal(code)
		}
		if out["machine_notice"] != tc.notice {
			t.Fatalf("notice %+v", out)
		}
	}
}

func TestChangingTheMachineResumesTheCancelledStep(t *testing.T) {
	f, guest, guestID := machineStudy(t)
	f.s.H.Engine = &flow.Engine{Store: f.s.H.Store, AgentRuns: f.s.H.AgentRuns}
	// Set sharing before creation so the initial leader can wait there.
	if code := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", f.jwt, f.ws, map[string]any{"visibility": "workspace"}, nil); code != 200 {
		t.Fatal(code)
	}
	var c card
	if code := f.s.call(t, "POST", "/api/cards", guest, f.ws, map[string]any{"predicates": testPredicates, "question": "q", "machine": "laptop"}, &c); code != 201 {
		t.Fatal(code)
	}
	if n := f.s.queuedRuns(t, c.ID); n != 1 {
		t.Fatal(n)
	}
	var affected map[string]any
	if code := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", f.jwt, f.ws, map[string]any{"visibility": "private"}, &affected); code != 409 {
		t.Fatalf("revoke preview %d", code)
	}
	// Confirmation uses the exact affected run ids from the read model.
	var runID string
	if err := f.s.Env.Owner.QueryRow(t.Context(), "SELECT id::text FROM agent_run WHERE card_id=$1", c.ID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if code := f.s.call(t, "PUT", "/api/runtimes/"+f.rt+"/sharing", f.jwt, f.ws, map[string]any{"visibility": "private", "confirm": []string{runID}}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := f.s.call(t, "PATCH", "/api/cards/"+c.ID+"/machine", guest, f.ws, map[string]any{"machine": "mine"}, nil); code != 200 {
		t.Fatal(code)
	}
	if n := f.s.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1 AND status='queued' AND role='leader' AND board_column='grounding' AND round=1 AND prompt LIKE '%Resumed on another machine.%'", c.ID); n != 1 {
		t.Fatalf("resumed %d", n)
	}
	if n := f.s.count(t, "SELECT count(*) FROM event WHERE card_id=$1 AND type='card.machine_changed'", c.ID); n != 1 {
		t.Fatalf("machine events %d", n)
	}
	token := f.s.personalToken(t, guest)
	var runtimes []db.Runtime
	if code := f.s.call(t, "POST", "/api/daemon/register", token, "", map[string]any{"workspace_id": f.ws, "daemon_id": "guest-machine", "device_name": "guest", "harnesses": []string{"claude", "codex"}}, &runtimes); code != 200 {
		t.Fatal(code)
	}
	refs := []service.RuntimeRef{}
	for _, rt := range runtimes {
		refs = append(refs, service.RuntimeRef{WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID})
	}
	claims, err := f.s.H.AgentRuns.Claim(t.Context(), uuid.MustParse(guestID), refs, 1)
	if err != nil || len(claims) != 1 || claims[0].AgentRun.CardID == nil || claims[0].AgentRun.CardID.String() != c.ID {
		t.Fatalf("own-machine claim %+v %v", claims, err)
	}
}

func TestChangingTheMachineLeavesARunningRunAlone(t *testing.T) {
	f, _, _ := machineStudy(t)
	f.s.H.Engine = &flow.Engine{Store: f.s.H.Store, AgentRuns: f.s.H.AgentRuns}
	var c card
	if code := f.s.call(t, "POST", "/api/cards", f.jwt, f.ws, map[string]any{"predicates": testPredicates, "question": "q"}, &c); code != 201 {
		t.Fatal(code)
	}
	if n := f.s.queuedRuns(t, c.ID); n != 1 {
		t.Fatal(n)
	}
	if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='running',runtime_id=$2 WHERE card_id=$1", c.ID, f.rt); err != nil {
		t.Fatal(err)
	}
	if code := f.s.call(t, "PATCH", "/api/cards/"+c.ID+"/machine", f.jwt, f.ws, map[string]any{"machine": "laptop"}, nil); code != 200 {
		t.Fatal(code)
	}
	if n := f.s.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1 AND status='running' AND runtime_id=$2", c.ID, f.rt); n != 1 {
		t.Fatalf("running %d", n)
	}
	if n := f.s.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1", c.ID); n != 1 {
		t.Fatalf("all runs %d", n)
	}
}

func TestAStudyDefaultsToMyMachines(t *testing.T) {
	f, _, _ := machineStudy(t)
	var out map[string]any
	if code := f.s.call(t, "POST", "/api/cards", f.jwt, f.ws, map[string]any{"predicates": testPredicates, "question": "my machines"}, &out); code != 201 {
		t.Fatal(code)
	}
	if out["machine"] != nil || out["machine_label"] != "My machines" || out["machine_notice"] != "" {
		t.Fatalf("default machine %+v", out)
	}
}

func TestUnsupportedRuntimeCannotBeSelectedForStudy(t *testing.T) {
	f, guest, _ := machineStudy(t)
	for _, platform := range []string{"darwin", "", "windows"} {
		if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET os=$1,visibility='workspace' WHERE id=$2", platform, f.rt); err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if code := f.s.call(t, "POST", "/api/cards", guest, f.ws, map[string]any{"predicates": testPredicates, "question": "unsupported", "machine": "skills-daemon"}, &out); code != 400 || out["error"] != "No machine named skills-daemon is shared with you" {
			t.Fatalf("%s: %d %+v", platform, code, out)
		}
	}
}
