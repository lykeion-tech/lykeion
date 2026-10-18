package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/service"
	"lykeion/server/internal/testutil"
	"lykeion/server/internal/toolroute"
)

// brokerRun uses the actual daemon claim/start protocol so private call authority
// is minted by the server, rather than being manufactured by the test.
func brokerRun(t *testing.T, s *server, jwt, ws string) service.Claimed {
	t.Helper()
	var c card
	if code := s.call(t, "POST", "/api/cards", jwt, ws, map[string]any{"question": "q"}, &c); code != 201 {
		t.Fatalf("card: %d", code)
	}
	personal := s.personalToken(t, jwt)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": ws, "daemon_id": "broker-" + c.ID, "harnesses": []string{"claude"}}, &rts); code != 200 {
		t.Fatalf("register: %d", code)
	}
	s.queue(t, ws, c.ID, s.userID(t, jwt), "claude")
	ref := map[string]any{"workspace_id": ws, "runtime_id": rts[0].ID}
	var claims []service.Claimed
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); code != 200 || len(claims) != 1 {
		t.Fatalf("claim: %d %+v", code, claims)
	}
	ref["attempt"] = claims[0].AgentRun.Attempt
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+claims[0].AgentRun.ID.String()+"/start", personal, "", ref, nil); code != 204 {
		t.Fatalf("start: %d", code)
	}
	return claims[0]
}

func brokerFixture(t *testing.T) (*server, string, string, mcpView, *httptest.Server) {
	t.Helper()
	s := newServer(t)
	jwt := s.signIn(t, "broker@lab.org")
	ws := s.workspace(t, jwt, "lab").ID
	remote := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	mcp.AddTool(remote, &mcp.Tool{Name: "echo", Description: "Actual source inventory"}, func(ctx context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "answer"}}}, nil, nil
	})
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{Stateless: true})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer remote-secret" {
			t.Error("missing upstream credentials")
			w.WriteHeader(401)
			return
		}
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	s.H.Connectors.HTTP = upstream.Client()
	var saved mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", jwt, ws, mcpInput("lab", 0, map[string]any{"url": upstream.URL, "headers": map[string]string{"Authorization": "Bearer remote-secret"}}), &saved); code != 201 {
		t.Fatalf("save: %d", code)
	}
	return s, jwt, ws, saved, upstream
}

func TestWorkspaceRemoteMCPStaysOnServer(t *testing.T) {
	s, jwt, ws, saved, upstream := brokerFixture(t)
	claim := brokerRun(t, s, jwt, ws)
	raw, _ := json.Marshal(claim)
	for _, secret := range []string{"remote-secret", upstream.URL} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("secret left server")
		}
	}
	var wire struct {
		WorkspaceConnectors []struct{ ID, Name, Transport string } `json:"workspace_connectors"`
	}
	json.Unmarshal(raw, &wire)
	if len(wire.WorkspaceConnectors) != 1 || wire.WorkspaceConnectors[0].ID != saved.ID {
		t.Fatalf("missing workspace claim: %s", raw)
	}
	var ts []toolroute.Tool
	path := "/api/role/mcp-servers/" + saved.ID
	if code := s.call(t, "POST", path+"/tools", claim.CallToken, "", map[string]any{}, &ts); code != 200 || len(ts) != 1 || ts[0].Name != "echo" {
		t.Fatalf("tools: %d %+v", code, ts)
	}
	var answer json.RawMessage
	if code := s.call(t, "POST", path+"/call", claim.CallToken, "", map[string]any{"tool": "echo", "args": map[string]any{}}, &answer); code != 200 || !strings.Contains(string(answer), "answer") {
		t.Fatalf("call: %d %s", code, answer)
	}
	if s.count(t, `SELECT count(*) FROM record WHERE kind='call'`) != 0 {
		t.Fatal("broker recorded duplicate")
	}
	if code := s.call(t, "POST", path+"/call", claim.CallToken, "", map[string]any{"tool": "echo", "url": "https://poison.invalid"}, nil); code != 400 {
		t.Fatalf("poison URL: %d", code)
	}
	if code := s.call(t, "POST", path+"/call", claim.CallToken, "", map[string]any{"tool": "forged"}, nil); code < 400 {
		t.Fatalf("forged tool: %d", code)
	}
}

func TestWorkspaceMCPBrokerRejectsAgentToken(t *testing.T) {
	s, jwt, ws, saved, _ := brokerFixture(t)
	claim := brokerRun(t, s, jwt, ws)
	for _, token := range []string{jwt, s.personalToken(t, jwt), claim.Token} {
		for _, suffix := range []string{"tools", "call"} {
			if code := s.call(t, "POST", "/api/role/mcp-servers/"+saved.ID+"/"+suffix, token, "", map[string]any{"tool": "echo"}, nil); code != 401 && code != 403 {
				t.Fatalf("non-door credential: %d", code)
			}
		}
	}
}

func TestWorkspaceMCPDoesNotEscapeTeamOrRunScope(t *testing.T) {
	s, jwt, ws, saved, _ := brokerFixture(t)
	claim := brokerRun(t, s, jwt, ws)
	var control []toolroute.Tool
	if code := s.call(t, "POST", "/api/role/mcp-servers/"+saved.ID+"/tools", claim.CallToken, "", map[string]any{}, &control); code != 200 || len(control) != 1 {
		t.Fatalf("scope control failed: %d %+v", code, control)
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if code := s.call(t, "POST", "/api/role/mcp-servers/"+id+"/tools", claim.CallToken, "", map[string]any{}, nil); code != 404 {
			t.Fatalf("unknown connector: %d", code)
		}
	}
	foreign := s.workspace(t, jwt, "foreign").ID
	other := brokerRun(t, s, jwt, foreign)
	if code := s.call(t, "POST", "/api/role/mcp-servers/"+saved.ID+"/tools", other.CallToken, "", map[string]any{}, nil); code != 404 {
		t.Fatalf("foreign connector: %d", code)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `DELETE FROM agent_run_token WHERE agent_run_id=$1`, claim.AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/role/mcp-servers/"+saved.ID+"/tools", claim.CallToken, "", map[string]any{}, nil); code != 401 {
		t.Fatalf("revoked token: %d", code)
	}
}

func TestWorkspaceMCPRoutingIgnoresForgedInventories(t *testing.T) {
	s, jwt, ws, saved, _ := brokerFixture(t)
	var command mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", jwt, ws, mcpInput("command", 0, map[string]any{"command": "fixture"}), &command); code != 201 {
		t.Fatal(code)
	}
	claim := brokerRun(t, s, jwt, ws)
	s.H.Engine.Jev = routingJev(t, func(q string) float64 {
		if strings.Contains(q, "workspace's own servers") || strings.Contains(q, `connector "lab"`) || strings.Contains(q, `connector "command"`) || strings.Contains(q, `tool "echo"`) || strings.Contains(q, `tool "actual"`) {
			return .9
		}
		return .1
	}, nil)
	fingerprint := service.CommandFingerprint([]string{"fixture"}, nil)
	body := map[string]any{"tools": []toolroute.Tool{{Name: "actual", Description: "daemon-listed"}}, "fingerprint": fingerprint}
	path := "/api/role/mcp-servers/" + command.ID + "/tools"
	if code := s.call(t, "POST", path, claim.Token, "", body, nil); code != 403 {
		t.Fatalf("agent published inventory: %d", code)
	}
	if code := s.call(t, "POST", path, claim.CallToken, "", body, nil); code != 200 {
		t.Fatalf("daemon inventory: %d", code)
	}
	var out toolsOut
	if code := s.call(t, "POST", "/api/role/tools", claim.Token, "", map[string]any{"need": "use workspace servers", "connector_tools": map[string][]toolroute.Tool{"lab": {{Name: "poisoned"}}, "command": {{Name: "poisoned"}}, "foreign": {{Name: "poisoned"}}}}, &out); code != 200 {
		t.Fatalf("routing: %d %s", code, out.Error)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "poisoned") {
		t.Fatalf("forged inventory routed: %s", raw)
	}
	// A forged tool is rejected by the actual remote source inventory too.
	if code := s.call(t, "POST", "/api/role/mcp-servers/"+saved.ID+"/call", claim.CallToken, "", map[string]any{"tool": "poisoned"}, nil); code != 400 {
		t.Fatalf("forged call: %d", code)
	}
	if !strings.Contains(string(raw), "echo") || !strings.Contains(string(raw), "actual") {
		t.Fatalf("actual inventories missing: %s", raw)
	}
}

func TestWorkspaceMCPTeamCannotCallUndeclaredLibrary(t *testing.T) {
	l := newTeamLab(t, 4)
	var saved mcpView
	if code := l.s.call(t, "POST", "/api/mcp-servers", l.jwt, l.ws, mcpInput("unbound", 0, map[string]any{"url": "https://example.org/mcp"}), &saved); code != 201 {
		t.Fatal(code)
	}
	run := l.claim(t)
	var raw json.RawMessage
	if code := l.s.call(t, "POST", "/api/role/mcp-servers/"+saved.ID+"/tools", run.callToken, "", map[string]any{}, &raw); code != 404 || !strings.Contains(string(raw), "MCP server not found") {
		t.Fatalf("undeclared Team connector: %d %s", code, raw)
	}
}

func TestWorkspaceMCPRoutingFencesReplacementDuringJev(t *testing.T) {
	s, jwt, ws, saved, upstream := brokerFixture(t)
	claim := brokerRun(t, s, jwt, ws)
	var once sync.Once
	s.H.Engine.Jev = routingJev(t, func(string) float64 { return .1 }, func() {
		once.Do(func() {
			if code := s.call(t, "PUT", "/api/mcp-servers/"+saved.ID, jwt, ws, mcpInput("lab", 1, map[string]any{"url": upstream.URL}), nil); code != 200 {
				t.Errorf("replace: %d", code)
			}
		})
	})
	var out json.RawMessage
	if code := s.call(t, "POST", "/api/role/tools", claim.Token, "", map[string]any{"need": "use workspace tools"}, &out); code < 400 {
		t.Fatalf("stale connector routing returned: %d %s", code, out)
	}
	if n := s.count(t, `SELECT count(*) FROM record WHERE kind='tools' AND agent_run_id=$1`, claim.AgentRun.ID); n != 0 {
		t.Fatalf("stale routing recorded: %d", n)
	}
}

func TestWorkspaceMCPUnapprovedCommandCannotRoutePoisonedInventory(t *testing.T) {
	s, jwt, ws, _, _ := brokerFixture(t)
	var command mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", jwt, ws, mcpInput("command", 0, map[string]any{"command": "fixture"}), &command); code != 201 {
		t.Fatal(code)
	}
	var uploaded struct{ ID string }
	archive := testutil.Pack(t, testutil.PackOpts{Name: "custom-team", Stage: "hypotheses", Members: 1, Connectors: map[string][]map[string]any{"pi": {{"name": "command", "workspace": command.ID}}}})
	if code := s.upload(t, jwt, ws, archive, &uploaded); code != 201 {
		t.Fatalf("upload: %d", code)
	}
	if code := s.call(t, "POST", "/api/teams/"+uploaded.ID+"/add", jwt, ws, nil, nil); code != 200 {
		t.Fatalf("add: %d", code)
	}
	l := &teamLab{s: s, jwt: jwt, ws: ws, personal: s.personalToken(t, jwt)}
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": ws, "daemon_id": "team", "harnesses": []string{"claude"}}, &rts); code != 200 {
		t.Fatal(code)
	}
	l.rts = rts[0].ID
	if code := s.call(t, "POST", "/api/cards", jwt, ws, map[string]any{"question": "q", "assignee": teamAssignee(map[string]string{"hypotheses": uploaded.ID})}, &l.card); code != 201 {
		t.Fatal(code)
	}
	run := l.claim(t)
	if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM team_agent WHERE workspace_id=$1 AND pack_id=$2`, ws, uploaded.ID); err != nil {
		t.Fatal(err)
	}
	s.H.Engine.Jev, _ = noulJev(t, .9)
	var out toolsOut
	if code := s.call(t, "POST", "/api/role/tools", run.token, "", map[string]any{"need": "command", "connector_tools": map[string][]toolroute.Tool{"command": {{Name: "poisoned"}}}}, &out); code != 200 {
		t.Fatalf("tools: %d %s", code, out.Error)
	}
	for _, tool := range out.Tools {
		if tool.Server == "command" {
			t.Fatalf("unapproved command routed: %+v", tool)
		}
	}
}

func TestWorkspaceMCPTeamCreationChecksReferenceOwnership(t *testing.T) {
	s, jwt, ws, saved, _ := brokerFixture(t)
	other := s.workspace(t, jwt, "other").ID
	var foreign mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", jwt, other, mcpInput("foreign", 0, map[string]any{"url": "https://foreign.example/mcp"}), &foreign); code != 201 {
		t.Fatalf("foreign connector: %d", code)
	}
	lead := s.newAgent(t, jwt, ws, "lead", "claude")
	aud := s.newAgent(t, jwt, ws, "aud", "codex")
	mem := s.newAgent(t, jwt, ws, "mem", "claude")
	for _, tc := range []struct {
		name, id              string
		formStatus, zipStatus int
	}{
		{"foreign", foreign.ID, 400, 404},
		{"missing", uuid.NewString(), 400, 404},
		{"owned", saved.ID, 201, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := formFor(lead, aud, mem)
			form["members"].([]any)[0].(map[string]any)["connectors"] = []any{map[string]any{"name": "library", "workspace": tc.id}}
			if code := s.call(t, "POST", "/api/teams", jwt, ws, form, nil); code != tc.formStatus {
				t.Fatalf("form: %d want %d", code, tc.formStatus)
			}
			zip := testutil.Pack(t, testutil.PackOpts{Name: "import-" + tc.name, Connectors: map[string][]map[string]any{"member-1": {{"name": "library", "workspace": tc.id}}}})
			if code := s.upload(t, jwt, ws, zip, nil); code != tc.zipStatus {
				t.Fatalf("ZIP: %d want %d", code, tc.zipStatus)
			}
		})
	}
	if count := s.count(t, `SELECT count(*) FROM field_pack`); count != 2 {
		t.Fatalf("unauthorized Team persisted: %d packs", count)
	}
}
