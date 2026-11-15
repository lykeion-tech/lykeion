package handler

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
)

func (s *server) personalToken(t *testing.T, jwt string) string {
	t.Helper()
	var out struct{ Token string }
	if got := s.call(t, "POST", "/api/tokens", jwt, "", map[string]string{"name": "daemon"}, &out); got != 201 {
		t.Fatalf("token: %d", got)
	}
	return out.Token
}

func (s *server) queue(t *testing.T, wsID, cardID, owner, harness string) {
	t.Helper()
	ws, c, o := uuid.MustParse(wsID), uuid.MustParse(cardID), uuid.MustParse(owner)
	err := s.H.Store.InWorkspace(context.Background(), ws, o, func(tx *store.Tx) error {
		_, err := s.H.AgentRuns.Enqueue(context.Background(), tx, o, service.NewAgentRun{Card: c, Column: "grounding", Round: 1, Role: "scoper", Harness: harness, Prompt: "scope it"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDaemonProtocolOverHTTP(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "a@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	c := s.idleCard(t, jwt, w.ID, "q")
	reg := map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude", "codex"}}
	if got := s.call(t, "POST", "/api/daemon/register", jwt, "", reg, nil); got != 401 {
		t.Fatalf("JWT daemon access: %d", got)
	}
	var rts []struct{ ID, Harness string }
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", reg, &rts); got != 200 || len(rts) != 2 {
		t.Fatalf("register: %d %+v", got, rts)
	}
	ref := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID}
	hint, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/api/daemon/ws", http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatalf("dial hint: %v %v", err, resp)
	}
	defer hint.Close()
	// Server sends readiness only after Redis subscription is live.
	hint.SetReadDeadline(time.Now().Add(3 * time.Second))
	var ready struct{ Type string }
	if err := hint.ReadJSON(&ready); err != nil || ready.Type != "ready" {
		t.Fatalf("hint ready: %+v %v", ready, err)
	}
	s.queue(t, w.ID, c.ID, s.userID(t, jwt), rts[0].Harness)
	var wake struct{ Type string }
	if err := hint.ReadJSON(&wake); err != nil || wake.Type != "agent_run_available" {
		t.Fatalf("hint wake: %+v %v", wake, err)
	}
	var claimed []struct {
		AgentRun struct {
			ID, Status string
			Attempt    int32
		} `json:"agent_run"`
		Token string
	}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []any{ref}, "slots": 2}, &claimed); got != 200 || len(claimed) != 1 || !strings.HasPrefix(claimed[0].Token, "ltk_") {
		t.Fatalf("claim: %d %+v", got, claimed)
	}
	id := claimed[0].AgentRun.ID
	if claimed[0].AgentRun.Attempt <= 0 {
		t.Fatalf("claim has no attempt: %+v", claimed)
	}
	path := "/api/daemon/agent-runs/" + id
	if got := s.call(t, "POST", path+"/start", tok, "", ref, nil); got != 400 {
		t.Fatalf("start without attempt: %d", got)
	}
	ref["attempt"] = claimed[0].AgentRun.Attempt
	for _, step := range []string{"prepare-lease", "start"} {
		if got := s.call(t, "POST", path+"/"+step, tok, "", ref, nil); got != 204 {
			t.Fatalf("%s: %d", step, got)
		}
	}
	if got := s.call(t, "POST", path+"/start", tok, "", ref, nil); got != 409 {
		t.Fatalf("second start: %d", got)
	}
	statusPath := path + "/status?workspace_id=" + w.ID + "&runtime_id=" + rts[0].ID
	if got := s.call(t, "GET", statusPath, tok, "", nil, nil); got != 400 {
		t.Fatalf("status without attempt: %d", got)
	}
	var status struct{ Status string }
	if got := s.call(t, "GET", statusPath+"&attempt=1", tok, "", nil, &status); got != 200 || status.Status != "running" {
		t.Fatalf("status: %d %+v", got, status)
	}
	for _, bad := range []map[string]any{{"failure_reason": "runtime_access_denied"}, {"failure_reason": "solar_flare"}, {"usage": []map[string]any{{"model": "m", "cost_usd": -1}}}} {
		bad["workspace_id"], bad["runtime_id"], bad["attempt"] = w.ID, rts[0].ID, claimed[0].AgentRun.Attempt
		if got := s.call(t, "POST", path+"/fail", tok, "", bad, nil); got != 400 {
			t.Fatalf("bad report %v: %d", bad, got)
		}
	}
	msg := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID, "attempt": claimed[0].AgentRun.Attempt, "messages": []map[string]any{{"seq": 1, "type": "text", "content": "hello"}}}
	if got := s.call(t, "POST", path+"/messages", tok, "", msg, nil); got != 204 {
		t.Fatalf("messages: %d", got)
	}
	var done struct{ Status string }
	report := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID, "attempt": claimed[0].AgentRun.Attempt, "usage": []map[string]any{{"model": "m", "input_tokens": 10, "output_tokens": 2, "cost_usd": 0.1}}}
	if got := s.call(t, "POST", path+"/complete", tok, "", report, &done); got != 200 || done.Status != "completed" {
		t.Fatalf("complete: %d %+v", got, done)
	}
	var run map[string]any
	if got := s.call(t, "GET", "/api/agent-runs/"+id, jwt, w.ID, nil, &run); got != 200 {
		t.Fatalf("get run: %d", got)
	}
	for _, k := range []string{"responsible_user_id", "queue_reason", "failure_reason", "cost_usd"} {
		if _, ok := run[k]; !ok {
			t.Fatalf("run missing %s: %v", k, run)
		}
	}
	if run["responsible_user_id"] != s.userID(t, jwt) || run["cost_usd"] != 0.1 {
		t.Fatalf("run: %v", run)
	}
	var msgs []map[string]any
	if got := s.call(t, "GET", "/api/agent-runs/"+id+"/messages?after=0", jwt, w.ID, nil, &msgs); got != 200 || len(msgs) != 1 {
		t.Fatalf("messages: %d %+v", got, msgs)
	}
	var list []map[string]any
	if got := s.call(t, "GET", "/api/runtimes", jwt, w.ID, nil, &list); got != 200 || len(list) != 2 {
		t.Fatalf("runtimes: %d %+v", got, list)
	}
}

func TestRuntimeCustomNameSurvivesRegistrationAndEmitsEvents(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "name@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	reg := map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude"}}
	var rts []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", reg, &rts); got != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", got, rts)
	}
	path := "/api/runtimes/" + rts[0].ID
	if got := s.call(t, "PATCH", path, jwt, w.ID, map[string]any{"custom_name": " Lab Mac ", "max_concurrent": 2}, nil); got != 204 {
		t.Fatalf("patch: %d", got)
	}
	reg["device_name"] = "renamed-host"
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", reg, nil); got != 200 {
		t.Fatalf("re-register: %d", got)
	}
	var list []struct {
		CustomName    *string `json:"custom_name"`
		DeviceName    string  `json:"device_name"`
		MaxConcurrent *int32  `json:"max_concurrent"`
	}
	if got := s.call(t, "GET", "/api/runtimes", jwt, w.ID, nil, &list); got != 200 || len(list) != 1 {
		t.Fatalf("list: %d %+v", got, list)
	}
	if list[0].CustomName == nil || *list[0].CustomName != "Lab Mac" || list[0].DeviceName != "renamed-host" || list[0].MaxConcurrent == nil || *list[0].MaxConcurrent != 2 {
		t.Fatalf("re-registration overwrote runtime settings: %+v", list[0])
	}
	for _, bad := range []map[string]any{{}, {"custom_name": strings.Repeat("x", 65)}, {"max_concurrent": 0}, {"max_concurrent": 33}} {
		if got := s.call(t, "PATCH", path, jwt, w.ID, bad, nil); got != 400 {
			t.Fatalf("bad patch %v: %d", bad, got)
		}
	}
	if got := s.call(t, "PATCH", path, jwt, w.ID, map[string]any{"custom_name": ""}, nil); got != 204 {
		t.Fatalf("clear name: %d", got)
	}
	s.call(t, "GET", "/api/runtimes", jwt, w.ID, nil, &list)
	if list[0].CustomName != nil {
		t.Fatalf("name not cleared: %q", *list[0].CustomName)
	}
	// null lifts the runtime's own limit, leaving its daemon's slots to decide.
	if got := s.call(t, "PATCH", path, jwt, w.ID, map[string]any{"max_concurrent": nil}, nil); got != 204 {
		t.Fatalf("clear limit: %d", got)
	}
	s.call(t, "GET", "/api/runtimes", jwt, w.ID, nil, &list)
	if list[0].MaxConcurrent != nil {
		t.Fatalf("limit not cleared: %d", *list[0].MaxConcurrent)
	}
	var updates int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='runtime.updated'", w.ID).Scan(&updates); err != nil {
		t.Fatal(err)
	}
	// One event per successful update: settings, the name cleared, the limit cleared.
	if updates != 3 {
		t.Fatalf("runtime.updated events: %d, want 3", updates)
	}
	if got := s.call(t, "DELETE", path, jwt, w.ID, nil, nil); got != 204 {
		t.Fatalf("delete: %d", got)
	}
	var deletes int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM event WHERE workspace_id=$1 AND type='runtime.deleted'", w.ID).Scan(&deletes); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("runtime.deleted events: %d, want 1", deletes)
	}
}

func TestDaemonCannotUseAnotherUsersRuntime(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	at, bt := s.personalToken(t, a), s.personalToken(t, b)
	w := s.workspace(t, a, "lab")
	if got := s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil); got != 200 {
		t.Fatalf("accept invitation: %d", got)
	}
	var rts []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", at, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "harnesses": []string{"claude"}}, &rts); got != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", got, rts)
	}
	ref := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID}
	for _, tc := range []struct {
		method, path, token, ws string
		body                    any
	}{
		{"POST", "/api/daemon/agent-runs/claim", bt, "", map[string]any{"runtimes": []any{ref}, "slots": 1}},
		{"POST", "/api/daemon/heartbeat", bt, "", map[string]any{"runtimes": []any{ref}}},
		{"POST", "/api/daemon/register", bt, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "harnesses": []string{"claude"}}},
		{"DELETE", "/api/runtimes/" + rts[0].ID, b, w.ID, nil},
		{"PATCH", "/api/runtimes/" + rts[0].ID, b, w.ID, map[string]any{"custom_name": "mine"}},
	} {
		if got := s.call(t, tc.method, tc.path, tc.token, tc.ws, tc.body, nil); got != 404 {
			t.Fatalf("%s %s as another user: %d", tc.method, tc.path, got)
		}
	}
}

func TestAgentRunMessagesPaginationRequiresRunAndAttempt(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "reader@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	c := s.idleCard(t, jwt, w.ID, "q")
	var rts []struct{ ID string }
	s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "harnesses": []string{"claude"}}, &rts)
	s.queue(t, w.ID, c.ID, s.userID(t, jwt), "claude")
	ref := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID}
	var claimed []struct {
		AgentRun struct {
			ID      string
			Attempt int32
		} `json:"agent_run"`
	}
	s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claimed)
	if len(claimed) != 1 {
		t.Fatalf("claim: %+v", claimed)
	}
	id := claimed[0].AgentRun.ID
	ref["attempt"] = claimed[0].AgentRun.Attempt
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+id+"/start", tok, "", ref, nil); got != 204 {
		t.Fatalf("start: %d", got)
	}
	msg := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID, "attempt": claimed[0].AgentRun.Attempt, "messages": []map[string]any{{"seq": 1, "type": "text", "content": "hello"}, {"seq": 2, "type": "text", "content": "world"}}}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+id+"/messages", tok, "", msg, nil); got != 204 {
		t.Fatalf("messages: %d", got)
	}
	var out []struct {
		Seq     int32
		Attempt int32
	}
	path := "/api/agent-runs/" + id + "/messages"
	if got := s.call(t, "GET", path+"?after=1&attempt=1", jwt, w.ID, nil, &out); got != 200 || len(out) != 1 || out[0].Seq != 2 || out[0].Attempt != 1 {
		t.Fatalf("paginated: %d %+v", got, out)
	}
	if got := s.call(t, "GET", path+"?after=-1", jwt, w.ID, nil, nil); got != 400 {
		t.Fatalf("negative after: %d", got)
	}
	if got := s.call(t, "GET", path+"?attempt=2", jwt, w.ID, nil, nil); got != 400 {
		t.Fatalf("future attempt: %d", got)
	}
	if got := s.call(t, "GET", "/api/agent-runs/"+uuid.NewString()+"/messages?attempt=0", jwt, w.ID, nil, nil); got != 404 {
		t.Fatalf("missing run should hide invalid query: %d", got)
	}
}

func TestPeopleCancelAndDaemonAcknowledges(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "cancel@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	c := s.idleCard(t, jwt, w.ID, "q")
	var workspaces []map[string]any
	if got := s.call(t, "GET", "/api/daemon/workspaces", tok, "", nil, &workspaces); got != 200 || len(workspaces) != 1 {
		t.Fatalf("daemon workspaces: %d %+v", got, workspaces)
	}
	var rts []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "harnesses": []string{"claude"}}, &rts); got != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", got, rts)
	}
	ref := map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID}
	var beat struct{ Touched int }
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": []any{ref}}, &beat); got != 200 || beat.Touched != 1 {
		t.Fatalf("heartbeat: %d %+v", got, beat)
	}
	s.queue(t, w.ID, c.ID, s.userID(t, jwt), "claude")
	var claimed []struct {
		AgentRun struct {
			ID      string
			Attempt int32
		} `json:"agent_run"`
	}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claimed); got != 200 || len(claimed) != 1 {
		t.Fatalf("claim: %d %+v", got, claimed)
	}
	id := claimed[0].AgentRun.ID
	ref["attempt"] = claimed[0].AgentRun.Attempt
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+id+"/start", tok, "", ref, nil); got != 204 {
		t.Fatalf("start: %d", got)
	}
	if got := s.call(t, "POST", "/api/agent-runs/"+id+"/cancel", jwt, w.ID, nil, nil); got != 204 {
		t.Fatalf("cancel: %d", got)
	}
	statusPath := "/api/daemon/agent-runs/" + id + "/status?workspace_id=" + w.ID + "&runtime_id=" + rts[0].ID + "&attempt=1"
	var status struct{ Status string }
	if got := s.call(t, "GET", statusPath, tok, "", nil, &status); got != 200 || status.Status != "cancelled" {
		t.Fatalf("status: %d %+v", got, status)
	}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+id+"/cancel-ack", tok, "", ref, nil); got != 204 {
		t.Fatalf("cancel ack: %d", got)
	}
	ref["attempt"] = int32(2)
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+id+"/cancel-ack", tok, "", ref, nil); got != 409 {
		t.Fatalf("wrong attempt: %d", got)
	}
}

func TestDaemonSocketSurvivesRequestTimeout(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "long-lived@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	c := s.idleCard(t, jwt, w.ID, "q")
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/api/daemon/ws", http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatalf("dial daemon socket: %v %v", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var ready struct{ Type string }
	if err := conn.ReadJSON(&ready); err != nil || ready.Type != "ready" {
		t.Fatalf("ready: %+v %v", ready, err)
	}
	// The old request bound cancelled the socket at ten seconds. Readiness makes
	// this wait start only after the owner subscription is established.
	time.Sleep(middleware.RequestTimeout + 250*time.Millisecond)
	s.queue(t, w.ID, c.ID, s.userID(t, jwt), "claude")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hint struct{ Type string }
	if err := conn.ReadJSON(&hint); err != nil || hint.Type != "agent_run_available" {
		t.Fatalf("hint after request deadline: %+v %v", hint, err)
	}
}

func TestDaemonUnavailableSubscriptionDoesNotAnnounceReady(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "hint@lab.org")
	tok := s.personalToken(t, jwt)
	s.H.Hub.Redis = nil
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/api/daemon/ws", http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var msg struct{ Type string }
	err = conn.ReadJSON(&msg)
	if err == nil && msg.Type == "ready" {
		t.Fatal("ready promised an unavailable subscription")
	}
}

func TestDaemonInvalidBatchNeverHidesCommittedClaims(t *testing.T) {
	for _, kind := range []string{"invalid", "membership", "deleted"} {
		for _, invalidFirst := range []bool{false, true} {
			t.Run(kind+fmt.Sprint(invalidFirst), func(t *testing.T) {
				s := newServer(t)
				jwt := s.signIn(t, "batch@lab.org")
				tok := s.personalToken(t, jwt)
				w := s.workspace(t, jwt, "batch")
				card := s.idleCard(t, jwt, w.ID, "q")
				var refs []struct{ ID string }
				if got := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "batch", "device_name": "test", "harnesses": []string{"claude"}}, &refs); got != 200 {
					t.Fatal(got)
				}
				s.queue(t, w.ID, card.ID, s.userID(t, jwt), "claude")
				valid := service.RuntimeRef{WorkspaceID: uuid.MustParse(w.ID), RuntimeID: uuid.MustParse(refs[0].ID)}
				bad := service.RuntimeRef{WorkspaceID: valid.WorkspaceID, RuntimeID: uuid.New()}
				if kind != "invalid" {
					other := s.workspace(t, jwt, "other")
					var rs []struct{ ID string }
					if got := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": other.ID, "daemon_id": "batch", "harnesses": []string{"claude"}}, &rs); got != 200 {
						t.Fatal(got)
					}
					bad = service.RuntimeRef{WorkspaceID: uuid.MustParse(other.ID), RuntimeID: uuid.MustParse(rs[0].ID)}
					query := "DELETE FROM runtime WHERE id=$1"
					args := []any{bad.RuntimeID}
					if kind == "membership" {
						query = "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2"
						args = []any{bad.WorkspaceID, uuid.MustParse(s.userID(t, jwt))}
					}
					if _, err := s.Env.Owner.Exec(t.Context(), query, args...); err != nil {
						t.Fatal(err)
					}
				}
				batch := []service.RuntimeRef{valid, bad}
				if invalidFirst {
					batch = []service.RuntimeRef{bad, valid}
				}
				if got := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": batch, "slots": 2}, nil); got != 404 {
					t.Fatalf("invalid batch=%d", got)
				}
				if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": batch}, nil); got != 404 {
					t.Fatalf("invalid heartbeat=%d", got)
				}
				var heartbeat struct{ Touched int }
				if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": []service.RuntimeRef{valid}}, &heartbeat); got != 200 || heartbeat.Touched != 1 {
					t.Fatalf("healthy heartbeat status=%d touched=%d", got, heartbeat.Touched)
				}
				var got []service.Claimed
				if status := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []service.RuntimeRef{valid}, "slots": 2}, &got); status != 200 || len(got) != 1 || got[0].AgentRun.Attempt != 1 || got[0].Token == "" {
					t.Fatalf("claim hidden by prior rejection: status=%d claims=%+v", status, got)
				}
			})
		}
	}
}

func TestAHeartbeatRecordsBaseStates(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "bases@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	var rts []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "harnesses": []string{"claude", "codex"}}, &rts); got != 200 || len(rts) != 2 {
		t.Fatalf("register: %d %+v", got, rts)
	}
	otherJWT := s.signIn(t, "neighbour@lab.org")
	otherTok := s.personalToken(t, otherJWT)
	ow := s.workspace(t, otherJWT, "neighbour")
	var others []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", otherTok, "", map[string]any{"workspace_id": ow.ID, "daemon_id": "d2", "harnesses": []string{"claude"}}, &others); got != 200 || len(others) != 1 {
		t.Fatalf("register other: %d %+v", got, others)
	}
	ref := []any{map[string]any{"workspace_id": w.ID, "runtime_id": rts[0].ID}, map[string]any{"workspace_id": w.ID, "runtime_id": rts[1].ID}}
	states := map[string]string{"python": "ready", "r": "failed: no network", "sandbox": "ready"}
	events := func() []string {
		t.Helper()
		var out []string
		rows, err := s.Env.Owner.Query(t.Context(), "SELECT payload->>'action' FROM event WHERE workspace_id=$1 AND type='runtime.updated' ORDER BY id", w.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			if err := rows.Scan(&a); err != nil {
				t.Fatal(err)
			}
			out = append(out, a)
		}
		return out
	}
	// listed is the bases every runtime in the workspace shows, or nil when
	// they differ.
	listed := func(jwt, ws string) map[string]string {
		t.Helper()
		var list []struct {
			Bases map[string]string `json:"bases"`
		}
		if got := s.call(t, "GET", "/api/runtimes", jwt, ws, nil, &list); got != 200 || len(list) == 0 {
			t.Fatalf("runtimes: %d %+v", got, list)
		}
		for _, r := range list[1:] {
			if !maps.Equal(r.Bases, list[0].Bases) {
				return nil
			}
		}
		return list[0].Bases
	}
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": ref, "bases": states}, nil); got != 200 {
		t.Fatalf("heartbeat: %d", got)
	}
	if got := listed(jwt, w.ID); !maps.Equal(got, states) {
		t.Fatalf("bases: %v", got)
	}
	if got := events(); !slices.Equal(got, []string{"bases"}) {
		t.Fatalf("events after first heartbeat: %v", got)
	}
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": ref, "bases": states}, nil); got != 200 {
		t.Fatalf("repeat heartbeat: %d", got)
	}
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": ref}, nil); got != 200 {
		t.Fatalf("bare heartbeat: %d", got)
	}
	if got := listed(jwt, w.ID); !maps.Equal(got, states) {
		t.Fatalf("bases after bare heartbeat: %v", got)
	}
	if got := events(); len(got) != 1 {
		t.Fatalf("unchanged bases emitted: %v", got)
	}
	foreign := map[string]any{"workspace_id": ow.ID, "runtime_id": others[0].ID}
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": []any{foreign}, "bases": states}, nil); got != 404 {
		t.Fatalf("foreign heartbeat: %d", got)
	}
	if got := listed(otherJWT, ow.ID); len(got) != 0 {
		t.Fatalf("foreign runtime changed: %v", got)
	}
	// A bad entry is dropped and the rest applied; a bases value that is not an
	// object is ignored. Neither fails the heartbeat or skips the runtimes.
	mixed := map[string]any{
		"python": "ready", "fortran": "ready", "r": "done", "julia": "failed: " + strings.Repeat("x", 201), "sandbox": "building",
	}
	var touched struct{ Touched int }
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": ref, "bases": mixed}, &touched); got != 200 || touched.Touched != 2 {
		t.Fatalf("mixed bases: %d %+v", got, touched)
	}
	if got := listed(jwt, w.ID); !maps.Equal(got, map[string]string{"python": "ready"}) {
		t.Fatalf("bases after mixed heartbeat: %v", got)
	}
	touched.Touched = 0
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": ref, "bases": []string{"ready"}}, &touched); got != 200 || touched.Touched != 2 {
		t.Fatalf("non-object bases: %d %+v", got, touched)
	}
	if got := listed(jwt, w.ID); !maps.Equal(got, map[string]string{"python": "ready"}) {
		t.Fatalf("bases after non-object heartbeat: %v", got)
	}
	if got := s.call(t, "POST", "/api/daemon/heartbeat", tok, "", map[string]any{"runtimes": ref, "bases": map[string]any{"r": "failed: " + strings.Repeat("x", 200), "julia": "failed: nul\x00", "python": 5, "sandbox": "failed: sandbox-exec missing"}}, nil); got != 200 {
		t.Fatalf("edge bases: %d", got)
	}
	if got := listed(jwt, w.ID); !maps.Equal(got, map[string]string{"r": "failed: " + strings.Repeat("x", 200), "sandbox": "failed: sandbox-exec missing"}) {
		t.Fatalf("bases after edge heartbeat: %v", got)
	}
}

func TestDaemonsCannotReportAccessDenied(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "denial@lab.org")
	tok := s.personalToken(t, jwt)
	ws := s.workspace(t, jwt, "denial").ID
	c := s.idleCard(t, jwt, ws, "q")
	var rts []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": ws, "daemon_id": "d", "device_name": "laptop", "harnesses": []string{"codex"}}, &rts); got != 200 {
		t.Fatal(got)
	}
	s.queue(t, ws, c.ID, s.userID(t, jwt), "codex")
	ref := map[string]any{"workspace_id": ws, "runtime_id": rts[0].ID}
	var claims []struct {
		AgentRun struct {
			ID      string
			Attempt int32
		} `json:"agent_run"`
	}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); got != 200 || len(claims) != 1 {
		t.Fatalf("claim %d %+v", got, claims)
	}
	ref["attempt"] = claims[0].AgentRun.Attempt
	ref["failure_reason"] = "runtime_access_denied"
	if got := s.call(t, "POST", "/api/daemon/agent-runs/"+claims[0].AgentRun.ID+"/fail", tok, "", ref, nil); got != 400 {
		t.Fatalf("daemon reported denial: %d", got)
	}
}
func TestEachRunKeepsItsMachine(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "snapshot@lab.org")
	tok := s.personalToken(t, jwt)
	ws := s.workspace(t, jwt, "snapshot").ID
	c := s.idleCard(t, jwt, ws, "q")
	var rts []struct{ ID string }
	if got := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": ws, "daemon_id": "d", "device_name": "laptop", "harnesses": []string{"codex"}}, &rts); got != 200 {
		t.Fatal(got)
	}
	s.queue(t, ws, c.ID, s.userID(t, jwt), "codex")
	ref := map[string]any{"workspace_id": ws, "runtime_id": rts[0].ID}
	var claims []struct {
		AgentRun struct{ ID string } `json:"agent_run"`
	}
	if got := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); got != 200 || len(claims) != 1 {
		t.Fatalf("claim %d %+v", got, claims)
	}
	if got := s.call(t, "DELETE", "/api/runtimes/"+rts[0].ID, jwt, ws, nil, nil); got != 204 {
		t.Fatalf("delete %d", got)
	}
	var name, owner string
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT machine_name,machine_owner_id::text FROM agent_run WHERE id=$1", claims[0].AgentRun.ID).Scan(&name, &owner); err != nil {
		t.Fatal(err)
	}
	if name != "laptop" || owner != s.userID(t, jwt) {
		t.Fatalf("machine snapshot %q %s", name, owner)
	}
}

func TestGuestClaimsRequireUpdatedDaemonProtocol(t *testing.T) {
	for _, capability := range []string{"", "unknown-protocol"} {
		for _, guestRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/guest_%t", capability, guestRun), func(t *testing.T) {
				f, guest, guestID := machineStudy(t)
				jwt, ownerID := f.jwt, f.s.userID(t, f.jwt)
				if guestRun {
					jwt, ownerID = guest, guestID
				}
				c := f.s.idleCard(t, jwt, f.ws, "protocol")
				if guestRun {
					if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET visibility='workspace' WHERE id=$1", f.rt); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.s.Env.Owner.Exec(t.Context(), "UPDATE card SET machine='skills-daemon' WHERE id=$1", c.ID); err != nil {
					t.Fatal(err)
				}
				f.s.queue(t, f.ws, c.ID, ownerID, "claude")
				body := map[string]any{"runtimes": []any{map[string]string{"workspace_id": f.ws, "runtime_id": f.rt}}, "slots": 1}
				if capability != "" {
					body["guest_isolation"] = capability
				}
				var claims []service.Claimed
				if code := f.s.call(t, "POST", "/api/daemon/agent-runs/claim", f.token, "", body, &claims); code != 200 {
					t.Fatalf("claim status %d", code)
				}
				want := 1
				if guestRun {
					want = 0
				}
				if len(claims) != want {
					t.Fatalf("claims=%d want=%d", len(claims), want)
				}
				if guestRun && (f.s.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1 AND status='queued' AND attempt=0", c.ID) != 1 || f.s.count(t, "SELECT count(*) FROM agent_run_token WHERE card_id=$1", c.ID) != 0) {
					t.Fatal("guest consumed attempt or token")
				}
			})
		}
	}
}
