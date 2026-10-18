package handler

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/service"
	"lykeion/server/internal/toolroute"
	"lykeion/server/internal/workspacemcp"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceAvailabilityPermissionsAndCAS(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "policy-owner@lab.org")
	ws := s.workspace(t, owner, "policy")
	member := s.member(t, owner, ws.ID, "policy-member@lab.org", "member")
	admin := s.member(t, owner, ws.ID, "policy-admin@lab.org", "admin")
	sk := createSkillFixture(t, s, member, ws.ID, "switchable-method")
	var custom mcpView
	if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput("switchable", 0, map[string]any{"url": "https://example.org/mcp"}), &custom); c != 201 {
		t.Fatal(c)
	}
	for _, item := range []struct{ kind, id, path string }{{"skill", sk.ID, "/api/skills"}, {"skill", "bundle:aeon", "/api/skills"}, {"mcp", custom.ID, "/api/mcp-servers"}} {
		t.Run(item.id, func(t *testing.T) {
			endpoint := "/api/availability/" + item.kind + "/" + item.id
			body := map[string]any{"revision": 0, "enabled": false}
			if c := s.call(t, "PUT", endpoint, member, ws.ID, body, nil); c != 403 {
				t.Fatalf("member write: %d", c)
			}
			var saved struct {
				Enabled  bool
				Revision int64
			}
			if c := s.call(t, "PUT", endpoint, admin, ws.ID, body, &saved); c != 200 || saved.Enabled || saved.Revision != 1 {
				t.Fatalf("disable: %d %+v", c, saved)
			}
			if c := s.call(t, "PUT", endpoint, owner, ws.ID, body, nil); c != 409 {
				t.Fatalf("stale: %d", c)
			}
			var rows []map[string]any
			if c := s.call(t, "GET", item.path, member, ws.ID, nil, &rows); c != 200 {
				t.Fatal(c)
			}
			found := false
			for _, row := range rows {
				if row["id"] == item.id {
					found = true
					if row["policy_enabled"] != false || row["policy_revision"] != float64(1) || row["revision"] != float64(1) {
						t.Fatalf("policy missing: %v", row)
					}
				}
			}
			if !found {
				t.Fatal("resource disappeared")
			}
			if c := s.call(t, "PUT", endpoint, owner, ws.ID, map[string]any{"revision": 1, "enabled": true}, &saved); c != 200 || !saved.Enabled || saved.Revision != 2 {
				t.Fatalf("enable: %d %+v", c, saved)
			}
		})
	}
}

func TestDisabledWorkspaceSkillsKeepExistingPins(t *testing.T) {
	l := newAgentLab(t)
	j, _ := noulJev(t, 0.9)
	l.s.H.Engine.Jev = j
	pinned := importRoutingSkill(t, l, "kept-method", []byte{1, 2})
	run := l.claim(t)
	first := routedFolder(t, l, run.token, "kept-method")
	future := importRoutingSkill(t, l, "disabled-method", []byte{3, 4})
	for _, id := range []string{pinned.ID, future.ID, "bundle:aeon"} {
		if c := l.s.call(t, "PUT", "/api/availability/skill/"+id, l.jwt, l.ws, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
			t.Fatal(c)
		}
	}
	if !bytes.Equal(first, routedFolder(t, l, run.token, "kept-method")) {
		t.Fatal("existing pin changed")
	}
	var out toolsOut
	if c := l.s.call(t, "POST", "/api/role/tools", run.token, "", map[string]any{"need": "use the imported method"}, &out); c != 200 {
		t.Fatal(c)
	}
	for _, s := range out.Skills {
		if s.Name == "disabled-method" || s.Name == "aeon" {
			t.Fatalf("disabled skill routed: %s", s.Name)
		}
	}
}

func TestDisabledCustomConnectorStopsNewCallsButFinishesStartedCall(t *testing.T) {
	s, jwt, ws, saved, upstream := brokerFixture(t)
	claim := brokerRun(t, s, jwt, ws)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var enteredOnce sync.Once
	defer once.Do(func() { close(release) })
	base := upstream.Client().Transport
	s.H.Connectors.HTTP.Transport = skillImportTransport(func(r *http.Request) (*http.Response, error) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		response, err := base.RoundTrip(r)
		if bytes.Contains(raw, []byte(`"tools/call"`)) {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		return response, err
	})
	endpoint := "/api/role/mcp-servers/" + saved.ID + "/call"
	done := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest("POST", s.URL+endpoint, strings.NewReader(`{"tool":"echo","args":{}}`))
		req.Header.Set("Authorization", "Bearer "+claim.CallToken)
		req.Header.Set("Content-Type", "application/json")
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			errs <- e
		} else {
			done <- r
		}
	}()
	select {
	case <-entered:
	case e := <-errs:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("call did not reach source")
	}
	if c := s.call(t, "PUT", "/api/availability/mcp/"+saved.ID, jwt, ws, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
		t.Fatal(c)
	}
	once.Do(func() { close(release) })
	select {
	case response := <-done:
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("started call: %d", response.StatusCode)
		}
		ticket := response.Header.Get(workspacemcp.RecordHeader)
		if ticket == "" {
			t.Fatal("missing receipt")
		}
		body := map[string]any{"server": "lab", "tool": "echo", "args": map[string]any{}, "answer": "answer", "workspace_call": ticket}
		if c := s.call(t, "POST", "/api/role/calls", claim.CallToken, "", body, nil); c != 201 {
			t.Fatalf("completed call receipt: %d", c)
		}
	case e := <-errs:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("started call stranded")
	}
	if c := s.call(t, "POST", endpoint, claim.CallToken, "", map[string]any{"tool": "echo", "args": map[string]any{}}, nil); c < 400 {
		t.Fatalf("disabled new call accepted: %d", c)
	}
}

func TestAvailabilityForeignWorkspaceAndAtomicEvent(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "policy-isolation@lab.org")
	ws := s.workspace(t, owner, "policy-isolation")
	other := s.workspace(t, owner, "other-policy")
	sk := createSkillFixture(t, s, owner, ws.ID, "isolated-method")
	endpoint := "/api/availability/skill/" + sk.ID
	if c := s.call(t, "PUT", endpoint, owner, other.ID, map[string]any{"revision": 0, "enabled": false}, nil); c != 404 {
		t.Fatalf("cross workspace: %d", c)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION refuse_policy_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.payload ? 'policy_revision' THEN RAISE EXCEPTION 'rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER refuse_policy_event BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION refuse_policy_event()`); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "PUT", endpoint, owner, ws.ID, map[string]any{"revision": 0, "enabled": false}, nil); c != 500 {
		t.Fatalf("event refusal: %d", c)
	}
	if s.count(t, `SELECT count(*) FROM workspace_availability`) != 0 {
		t.Fatal("policy committed without event")
	}
}

func TestDisabledSkillCannotEnterNewTeamSelection(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "policy-team@lab.org")
	ws := s.workspace(t, jwt, "policy-team")
	sk := createSkillFixture(t, s, jwt, ws.ID, "team-switchable")
	for _, item := range []struct{ id, name string }{{sk.ID, sk.Name}, {"bundle:aeon", "aeon"}} {
		if c := s.call(t, "PUT", "/api/availability/skill/"+item.id, jwt, ws.ID, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
			t.Fatal(c)
		}
		if _, err := s.H.skillService().SelectForTeam(t.Context(), uuid.MustParse(ws.ID), uuid.MustParse(s.userID(t, jwt)), []string{item.name}); err == nil {
			t.Fatalf("disabled %s entered new Team", item.name)
		}
	}
}

func TestAvailabilityUUIDAliasesSharePolicy(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "alias-policy@lab.org")
	ws := s.workspace(t, jwt, "aliases")
	sk := createSkillFixture(t, s, jwt, ws.ID, "alias-method")
	var custom mcpView
	if c := s.call(t, "POST", "/api/mcp-servers", jwt, ws.ID, mcpInput("alias", 0, map[string]any{"command": "fixture"}), &custom); c != 201 {
		t.Fatal(c)
	}
	for _, item := range []struct{ kind, id, path string }{{"skill", sk.ID, "/api/skills/"}, {"mcp", custom.ID, "/api/mcp-servers/"}} {
		aliases := []string{strings.ToUpper(item.id), strings.ReplaceAll(item.id, "-", ""), "{" + item.id + "}"}
		for i, alias := range aliases {
			enabled := i%2 != 0
			if c := s.call(t, "PUT", "/api/availability/"+item.kind+"/"+alias, jwt, ws.ID, map[string]any{"enabled": enabled, "revision": i}, nil); c != 200 {
				t.Fatalf("alias %s: %d", alias, c)
			}
			var got map[string]any
			if item.kind == "skill" {
				if c := s.call(t, "GET", item.path+item.id, jwt, ws.ID, nil, &got); c != 200 {
					t.Fatal(c)
				}
			} else {
				var rows []map[string]any
				if c := s.call(t, "GET", "/api/mcp-servers", jwt, ws.ID, nil, &rows); c != 200 {
					t.Fatal(c)
				}
				for _, row := range rows {
					if row["id"] == item.id {
						got = row
					}
				}
			}
			if got["policy_enabled"] != enabled || got["policy_revision"] != float64(i+1) {
				t.Fatalf("canonical read: %v", got)
			}
			if c := s.call(t, "PUT", "/api/availability/"+item.kind+"/"+item.id, jwt, ws.ID, map[string]any{"enabled": true, "revision": i}, nil); c != 409 {
				t.Fatalf("alias stale CAS: %d", c)
			}
			if item.kind == "skill" {
				if c := s.call(t, "GET", item.path+alias, jwt, ws.ID, nil, &got); c != 200 || got["policy_enabled"] != enabled {
					t.Fatalf("alias detail: %d %v", c, got)
				}
			}
		}
	}
}

func TestDisabledCustomConnectorCannotEnterNewTeam(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "connector-team@lab.org")
	ws := s.workspace(t, jwt, "team-policy")
	lead, aud, mem := s.newAgent(t, jwt, ws.ID, "lead", "claude"), s.newAgent(t, jwt, ws.ID, "aud", "codex"), s.newAgent(t, jwt, ws.ID, "mem", "claude")
	var custom mcpView
	if c := s.call(t, "POST", "/api/mcp-servers", jwt, ws.ID, mcpInput("team-connector", 0, map[string]any{"command": "fixture"}), &custom); c != 201 {
		t.Fatal(c)
	}
	form := formFor(lead, aud, mem)
	form["leader"].(map[string]any)["connectors"] = []any{map[string]any{"name": "team-connector", "workspace": custom.ID}}
	if c := s.call(t, "PUT", "/api/availability/mcp/"+custom.ID, jwt, ws.ID, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", "/api/teams", jwt, ws.ID, form, nil); c != 400 {
		t.Fatalf("disabled connector entered Team: %d", c)
	}
}

func TestCommandCompletionRequiresPriorEnabledAuthorization(t *testing.T) {
	s, jwt, ws, _, _ := brokerFixture(t)
	var command mcpView
	if c := s.call(t, "POST", "/api/mcp-servers", jwt, ws, mcpInput("command", 0, map[string]any{"command": "fixture"}), &command); c != 201 {
		t.Fatal(c)
	}
	claim := brokerRun(t, s, jwt, ws)
	path := "/api/role/mcp-servers/" + command.ID + "/tools"
	fingerprint := service.CommandFingerprint([]string{"fixture"}, nil)
	if c := s.call(t, "POST", path, claim.CallToken, "", map[string]any{"tools": []toolroute.Tool{{Name: "actual"}}, "fingerprint": fingerprint}, nil); c != 200 {
		t.Fatal(c)
	}
	request := func(body any) (int, string) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", s.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+claim.CallToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header.Get(workspacemcp.RecordHeader)
	}
	args := map[string]any{"value": "original"}
	c, start := request(map[string]any{"fingerprint": fingerprint, "begin": map[string]any{"tool": "actual", "args": args}})
	if c != 200 || start == "" {
		t.Fatalf("begin authorization: %d ticket=%t", c, start != "")
	}
	if c := s.call(t, "PUT", "/api/availability/mcp/"+command.ID, jwt, ws, map[string]any{"enabled": false, "revision": 0}, nil); c != 200 {
		t.Fatal(c)
	}
	if c, _ := request(map[string]any{"fingerprint": fingerprint, "begin": map[string]any{"tool": "actual", "args": args}}); c < 400 {
		t.Fatalf("new disabled command authorized: %d", c)
	}
	record := map[string]any{"tool": "actual", "args": args, "answer_sha256": workspacemcp.AnswerHash("completed")}
	if c, _ := request(map[string]any{"fingerprint": fingerprint, "record": record}); c < 400 {
		t.Fatal("completion without start accepted")
	}
	bad := map[string]any{"tool": "actual", "args": map[string]any{"value": "substituted"}, "answer_sha256": workspacemcp.AnswerHash("completed")}
	if c, _ := request(map[string]any{"fingerprint": fingerprint, "record": bad, "start": start}); c < 400 {
		t.Fatal("substituted completion accepted")
	}
	c, ticket := request(map[string]any{"fingerprint": fingerprint, "record": record, "start": start})
	if c != 200 || ticket == "" {
		t.Fatalf("started command completion: %d ticket=%t", c, ticket != "")
	}
	if c := s.call(t, "POST", "/api/role/calls", claim.CallToken, "", map[string]any{"server": "command", "tool": "actual", "args": args, "answer": "completed", "workspace_call": ticket}, nil); c != 201 {
		t.Fatalf("completed evidence: %d", c)
	}
}
