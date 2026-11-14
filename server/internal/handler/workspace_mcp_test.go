package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type mcpView struct {
	ID, Name, Transport, CreatedBy string
	Revision                       int64
}

func mcpInput(name string, revision int64, config map[string]any) map[string]any {
	return map[string]any{"name": name, "revision": revision, "config": config}
}
func TestWorkspaceMCPCRUDIsAdminOnly(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	member := s.member(t, owner, ws.ID, "member@lab.org", "member")
	admin := s.member(t, owner, ws.ID, "admin@lab.org", "admin")
	body := mcpInput("remote", 0, map[string]any{"url": "https://example.org/mcp"})
	if c := s.call(t, "POST", "/api/mcp-servers", member, ws.ID, body, nil); c != 403 {
		t.Fatalf("member create: %d", c)
	}
	var got mcpView
	if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, body, &got); c != 201 || got.Revision != 1 || got.Transport != "url" {
		t.Fatalf("create: %d %+v", c, got)
	}
	path := "/api/mcp-servers/" + got.ID
	for _, method := range []string{"PUT", "DELETE"} {
		if c := s.call(t, method, path, member, ws.ID, body, nil); c != 403 {
			t.Fatalf("member %s: %d", method, c)
		}
	}
	var list []mcpView
	if c := s.call(t, "GET", "/api/mcp-servers", member, ws.ID, nil, &list); c != 200 || len(list) != 1 {
		t.Fatalf("member list: %d %+v", c, list)
	}
	body = mcpInput("renamed", 1, map[string]any{"command": "npx", "args": []string{"-y", "server"}, "env": map[string]string{"MODE": "fast"}})
	if c := s.call(t, "PUT", path, admin, ws.ID, body, &got); c != 200 || got.Revision != 2 || got.Transport != "command" {
		t.Fatalf("admin update: %d %+v", c, got)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		var conflict struct {
			Code     string
			Revision int64
		}
		staleBody := body
		if method == "DELETE" {
			staleBody = map[string]any{"revision": 1}
		}
		if c := s.call(t, method, path, owner, ws.ID, staleBody, &conflict); c != 409 || conflict.Code != "stale_revision" || conflict.Revision != 2 {
			t.Fatalf("stale %s: %d %+v", method, c, conflict)
		}
	}
	if c := s.call(t, "DELETE", path, admin, ws.ID, map[string]any{"revision": 2}, nil); c != 204 {
		t.Fatalf("admin delete: %d", c)
	}
	if s.count(t, `SELECT count(*) FROM workspace_mcp_server`) != 0 {
		t.Fatal("delete retained row")
	}
}
func TestWorkspaceMCPConfigNeverReadsBack(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	const secret = "bearer-private-secret"
	config := map[string]any{"url": "https://example.org/mcp?token=" + secret, "headers": map[string]string{"Authorization": "Bearer " + secret}}
	var raw json.RawMessage
	if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput("remote", 0, config), &raw); c != 201 {
		t.Fatalf("create: %d %s", c, raw)
	}
	var got mcpView
	json.Unmarshal(raw, &got)
	assertSummary := func(raw []byte) {
		t.Helper()
		for _, field := range []string{secret, `"config":`, `"url":`, `"command":`, `"args":`, `"headers":`, `"env":`} {
			if strings.Contains(string(raw), field) {
				t.Fatalf("configuration leaked: %s", raw)
			}
		}
	}
	assertSummary(raw)
	s.call(t, "GET", "/api/mcp-servers", owner, ws.ID, nil, &raw)
	assertSummary(raw)
	var ciphertext []byte
	if err := s.Env.Owner.QueryRow(context.Background(), `SELECT config_ciphertext FROM workspace_mcp_server WHERE id=$1`, got.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), secret) || strings.Contains(string(ciphertext), "https://") {
		t.Fatal("config was not sealed")
	}
	if s.count(t, `SELECT count(*) FROM event WHERE type LIKE 'connector.%' AND (payload::text LIKE '%' || $1 || '%' OR payload ? 'config' OR payload ? 'url' OR payload ? 'command' OR payload ? 'args' OR payload ? 'env' OR payload ? 'headers')`, secret) != 0 {
		t.Fatal("event leaked configuration")
	}
	if c := s.call(t, "PUT", "/api/mcp-servers/"+got.ID, owner, ws.ID, mcpInput("remote", 1, map[string]any{"url": "https://replacement.org/mcp"}), &raw); c != 200 {
		t.Fatalf("replace: %d %s", c, raw)
	}
	assertSummary(raw)
	if err := s.Env.Owner.QueryRow(context.Background(), `SELECT config_ciphertext FROM workspace_mcp_server WHERE id=$1`, got.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	saved, err := s.H.Connectors.Open(uuid.MustParse(ws.ID), "mcp-servers/"+got.ID, ciphertext)
	if err != nil || strings.Contains(saved, secret) || strings.Contains(saved, "headers") {
		t.Fatalf("full replacement did not clear prior config: %v", err)
	}
}
func TestCommandMCPRejectsSecrets(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	for _, key := range []string{"API_KEY", "token", "MY_Secret", "PASSWORD"} {
		var raw json.RawMessage
		c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput("command", 0, map[string]any{"command": "npx", "args": []string{"server"}, "env": map[string]string{key: "private-secret"}}), &raw)
		if c != 400 || strings.Contains(string(raw), "private-secret") {
			t.Fatalf("secret env: %d %s", c, raw)
		}
	}
}
func TestConnectorStaleSaveRejected(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	if c := s.call(t, "PUT", "/api/connectors/ensembl", owner, ws.ID, map[string]any{"enabled": false}, nil); c != 400 {
		t.Fatalf("missing revision: %d", c)
	}
	var absent struct {
		Code     string
		Revision int64
	}
	if c := s.call(t, "PUT", "/api/connectors/omim", owner, ws.ID, map[string]any{"revision": 9, "enabled": true}, &absent); c != 409 || absent.Code != "stale_revision" || absent.Revision != 0 {
		t.Fatalf("absent stale: %d %+v", c, absent)
	}
	if s.count(t, `SELECT count(*) FROM workspace_connector WHERE name='omim'`) != 0 {
		t.Fatal("stale absent save created configuration")
	}
	var got struct {
		Revision int64
		Enabled  bool
	}
	if c := s.call(t, "PUT", "/api/connectors/ensembl", owner, ws.ID, map[string]any{"revision": 0, "enabled": false}, &got); c != 200 || got.Revision != 1 || !got.Enabled {
		t.Fatalf("initial revision: %d %+v", c, got)
	}
	var conflict struct {
		Code     string
		Revision int64
	}
	if c := s.call(t, "PUT", "/api/connectors/ensembl", owner, ws.ID, map[string]any{"revision": 0, "enabled": true}, &conflict); c != 409 || conflict.Code != "stale_revision" || conflict.Revision != 1 {
		t.Fatalf("stale: %d %+v", c, conflict)
	}
	if s.count(t, `SELECT count(*) FROM workspace_connector WHERE name='ensembl' AND revision=1 AND enabled`) != 1 {
		t.Fatal("CAS changed saved state")
	}
	if c := s.call(t, "PUT", "/api/connectors/ensembl", owner, ws.ID, map[string]any{"revision": 1, "enabled": true}, &got); c != 200 || got.Revision != 2 {
		t.Fatalf("second revision: %d %+v", c, got)
	}
}

func TestWorkspaceMCPRunTokensAreRefused(t *testing.T) {
	s := newServer(t)
	_, ws, _, role := s.roleFixture(t)
	if c := s.call(t, "PUT", "/api/connectors/ensembl", role.token, ws, map[string]any{"revision": 0, "enabled": false}, nil); c != 401 {
		t.Fatalf("run token catalogue write: %d", c)
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		path := "/api/mcp-servers"
		if method != "POST" {
			path += "/00000000-0000-0000-0000-000000000001"
		}
		if c := s.call(t, method, path, role.token, ws, mcpInput("remote", 1, map[string]any{"url": "https://example.org/mcp"}), nil); c != 401 {
			t.Fatalf("run token %s: %d", method, c)
		}
	}
	if s.count(t, `SELECT count(*) FROM workspace_mcp_server`) != 0 {
		t.Fatal("run actor wrote config")
	}
}

func TestWorkspaceCommandReportsExplicitKeylessDeclaration(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "keyless@lab.org")
	ws := s.workspace(t, owner, "lab")
	var created map[string]any
	if code := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput("keyless", 0, map[string]any{"command": "node", "args": []string{"server.js"}, "keyless": true}), &created); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var rows []map[string]any
	if code := s.call(t, "GET", "/api/mcp-servers", owner, ws.ID, nil, &rows); code != 200 || len(rows) != 1 || rows[0]["keyless"] != true {
		t.Fatalf("list: %d %+v", code, rows)
	}
}
func TestWorkspaceMCPForeignAndRemovedMembersCannotMutate(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	admin := s.member(t, owner, ws.ID, "admin@lab.org", "admin")
	var got mcpView
	body := mcpInput("remote", 0, map[string]any{"url": "https://example.org/mcp"})
	if c := s.call(t, "POST", "/api/mcp-servers", admin, ws.ID, body, &got); c != 201 {
		t.Fatal(c)
	}
	foreign := s.signIn(t, "foreign@lab.org")
	other := s.workspace(t, foreign, "other")
	if c := s.call(t, "DELETE", "/api/mcp-servers/"+got.ID, foreign, other.ID, map[string]any{"revision": 1}, nil); c != 404 {
		t.Fatalf("cross tenant: %d", c)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `UPDATE member SET role='member' WHERE workspace_id=$1 AND user_id=(SELECT id FROM "user" WHERE email='admin@lab.org')`, ws.ID); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "DELETE", "/api/mcp-servers/"+got.ID, admin, ws.ID, map[string]any{"revision": 1}, nil); c != 403 {
		t.Fatalf("demoted admin: %d", c)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=(SELECT id FROM "user" WHERE email='admin@lab.org')`, ws.ID); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "GET", "/api/mcp-servers", admin, ws.ID, nil, nil); c != 404 {
		t.Fatalf("removed member: %d", c)
	}
}
func TestWorkspaceMCPInvalidInputNeverEchoesConfig(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	for _, config := range []map[string]any{
		{"url": "http://private-secret.example.org"}, {"command": "/private-secret"}, {"url": "https://example.org", "command": "private-secret"},
		{"private-secret": "value"}, {"url": map[string]string{"private-secret": "value"}},
	} {
		var raw json.RawMessage
		if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput("remote", 0, config), &raw); c != 400 || strings.Contains(string(raw), "private-secret") {
			t.Fatalf("invalid config: %d %s", c, raw)
		}
	}
}

func TestWorkspaceMCPNamesFollowSourceTrim(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	var got mcpView
	command := map[string]any{"command": "npx", "args": []string{"  --mode=fast path  ", "\targ\n"}, "env": map[string]string{"MODE": "  fast  "}}
	if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput(" \tremote \n", 0, command), &got); c != 201 || got.Name != "remote" {
		t.Fatalf("trimmed create: %d %+v", c, got)
	}
	readConfig := func() map[string]any {
		t.Helper()
		var ciphertext []byte
		if err := s.Env.Owner.QueryRow(context.Background(), `SELECT config_ciphertext FROM workspace_mcp_server WHERE id=$1`, got.ID).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		raw, err := s.H.Connectors.Open(uuid.MustParse(ws.ID), "mcp-servers/"+got.ID, ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
		return config
	}
	stored := readConfig()
	args := stored["args"].([]any)
	if args[0] != "  --mode=fast path  " || args[1] != "\targ\n" || stored["env"].(map[string]any)["MODE"] != "  fast  " {
		t.Fatal("command configuration was normalized")
	}
	remote := map[string]any{"url": "https://example.org/mcp", "headers": map[string]string{"X-Mode": "  fast  "}}
	if c := s.call(t, "PUT", "/api/mcp-servers/"+got.ID, owner, ws.ID, mcpInput(" \tnew_name \r", 1, remote), &got); c != 200 || got.Name != "new_name" {
		t.Fatalf("trimmed update: %d %+v", c, got)
	}
	if readConfig()["headers"].(map[string]any)["X-Mode"] != "  fast  " {
		t.Fatal("header value was normalized")
	}
}

func TestAWorkspaceMCPCannotBeNamedAttachments(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	remote := map[string]any{"url": "https://example.org/mcp"}
	const want = `the name "attachments" is reserved for a study's attached files`
	for _, name := range []string{"attachments", " ATTACHMENTS\t"} {
		var out struct{ Error string }
		if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput(name, 0, remote), &out); c != 400 || out.Error != want {
			t.Fatalf("create %q: %d %q", name, c, out.Error)
		}
	}
	var got mcpView
	if c := s.call(t, "POST", "/api/mcp-servers", owner, ws.ID, mcpInput("remote", 0, remote), &got); c != 201 {
		t.Fatalf("create: %d", c)
	}
	var out struct{ Error string }
	if c := s.call(t, "PUT", "/api/mcp-servers/"+got.ID, owner, ws.ID, mcpInput("Attachments", 1, remote), &out); c != 400 || out.Error != want {
		t.Fatalf("rename: %d %q", c, out.Error)
	}
}
