package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TestEveryWorkspaceRouteHidesOtherWorkspaces audits every registered API
// route that operates on workspace data. Workspace routes use A's workspace
// URL; resource routes use B's scope with real identifiers owned by A.
func TestWorkspaceRouteIsolation(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@else.org")
	wa := s.workspace(t, a, "lab-a")
	wb := s.workspace(t, b, "lab-b")
	c := s.idleCard(t, a, wa.ID, "q")
	var research, agent struct{ ID string }
	if code := s.call(t, "POST", "/api/researches", a, wa.ID, map[string]string{"name": "Isolation research"}, &research); code != http.StatusCreated || research.ID == "" {
		t.Fatalf("research fixture: %d %+v", code, research)
	}
	if code := s.call(t, "POST", "/api/agents", a, wa.ID, map[string]string{"name": "Isolation agent", "harness": "codex"}, &agent); code != http.StatusCreated || agent.ID == "" {
		t.Fatalf("agent fixture: %d %+v", code, agent)
	}
	var mcp mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", a, wa.ID, mcpInput("isolation-mcp", 0, map[string]any{"url": "https://example.org/mcp"}), &mcp); code != http.StatusCreated || mcp.ID == "" {
		t.Fatalf("MCP fixture: %d %+v", code, mcp)
	}
	var ownMCP []mcpView
	if code := s.call(t, "GET", "/api/mcp-servers", a, wa.ID, nil, &ownMCP); code != http.StatusOK || len(ownMCP) != 1 || ownMCP[0].ID != mcp.ID {
		t.Fatalf("owner cannot list MCP fixture: %d %+v", code, ownMCP)
	}
	personal := s.personalToken(t, a)
	var runtimes []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{
		"workspace_id": wa.ID, "daemon_id": "isolation-daemon", "harnesses": []string{"claude"},
	}, &runtimes); code != http.StatusOK || len(runtimes) != 1 || runtimes[0].ID == "" {
		t.Fatalf("register runtime fixture: %d %+v", code, runtimes)
	}
	runtimeID := runtimes[0].ID
	s.queue(t, wa.ID, c.ID, s.userID(t, a), "claude")
	var runID uuid.UUID
	var originalStatus string
	var originalAttempt int32
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT id, status, attempt FROM agent_run
		WHERE workspace_id = $1 AND card_id = $2`, wa.ID, c.ID).Scan(&runID, &originalStatus, &originalAttempt); err != nil {
		t.Fatal(err)
	}
	if runID == uuid.Nil || originalStatus != "queued" || originalAttempt != 0 {
		t.Fatalf("invalid agent-run fixture: id=%s status=%s attempt=%d", runID, originalStatus, originalAttempt)
	}
	var item string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
		VALUES ($1, $2, $3, 'escalated', 'x') RETURNING id`, wa.ID, c.ID, s.userID(t, a)).Scan(&item); err != nil {
		t.Fatal(err)
	}
	var attached string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO attachment (workspace_id, uploader_id, card_id, name, content_type, size_bytes, sha256, blob_key)
		VALUES ($1, $2, $3, 'a.txt', 'text/plain', 1, 'x', $4) RETURNING id`, wa.ID, s.userID(t, a), c.ID, wa.ID+"/attachments/probe").Scan(&attached); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Blobs.Put(t.Context(), wa.ID+"/attachments/probe", strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	if resp, body := s.download(t, a, wa.ID, c.ID, attached); resp.StatusCode != 200 || string(body) != "x" {
		t.Fatalf("owner download: %d", resp.StatusCode)
	}
	var cs csProject
	if code := s.call(t, "POST", csProjects, a, wa.ID, map[string]string{"name": "Isolation Coresearch"}, &cs); code != 201 {
		t.Fatalf("Coresearch fixture: %d", code)
	}
	code, version := csUpload(t, s, a, wa.ID, cs.ID, "fixture.tex", 0, false, []byte("fixture"))
	if code != 201 {
		t.Fatalf("Coresearch file fixture: %d", code)
	}

	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_project SET mode='cowrite' WHERE id=$1`, cs.ID); err != nil {
		t.Fatal(err)
	}
	docURL := csProjects + "/" + cs.ID + "/docs/" + version.FileID.String()
	for _, suffix := range []string{"", "/steps?after=0", "/presence"} {
		if code := s.call(t, "GET", docURL+suffix, a, wa.ID, nil, nil); code != 200 {
			t.Fatalf("owner document %s: %d", suffix, code)
		}
	}
	if code := s.call(t, "POST", docURL+"/cursor", a, wa.ID, map[string]int{"anchor": 0, "head": 0}, nil); code != 204 {
		t.Fatalf("owner cursor: %d", code)
	}
	if code := s.call(t, "POST", docURL+"/steps", a, wa.ID, map[string]any{"base": 0, "client_id": "owner", "steps": []any{[]any{7}}}, nil); code != 200 {
		t.Fatalf("owner document step: %d", code)
	}
	var welcome struct{ Conversation struct{ ID string } }
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, wa.ID, nil, &welcome); code != 200 || welcome.Conversation.ID == "" {
		t.Fatalf("conversation fixture %d", code)
	}
	ids := map[string]string{"{conversation}": welcome.Conversation.ID, "{project}": cs.ID, "{file}": version.FileID.String(), "{n}": "1", "{chat}": c.ID, "{cell}": c.ID, "{key}": "R1", "{aid}": attached, "{id}": c.ID, "{user}": s.userID(t, a), "{inv}": c.ID, "{token}": "unknown-token"}
	// Positive controls prove the probed resources resolve for their owner.
	var ownRun struct {
		ID     string
		Status string
	}
	if code := s.call(t, http.MethodGet, "/api/agent-runs/"+runID.String(), a, wa.ID, nil, &ownRun); code != http.StatusOK || ownRun.ID != runID.String() || ownRun.Status != originalStatus {
		t.Fatalf("owner cannot read agent-run fixture: %d %+v", code, ownRun)
	}
	var ownMessages []map[string]any
	if code := s.call(t, http.MethodGet, "/api/agent-runs/"+runID.String()+"/messages", a, wa.ID, nil, &ownMessages); code != http.StatusOK || len(ownMessages) != 0 {
		t.Fatalf("owner cannot read agent-run messages: %d %+v", code, ownMessages)
	}
	var ownRuntimes []struct{ ID string }
	if code := s.call(t, http.MethodGet, "/api/runtimes", a, wa.ID, nil, &ownRuntimes); code != http.StatusOK || len(ownRuntimes) != 1 || ownRuntimes[0].ID != runtimeID {
		t.Fatalf("owner cannot list runtime fixture: %d %+v", code, ownRuntimes)
	}
	if code := s.call(t, http.MethodPatch, "/api/runtimes/"+runtimeID, a, wa.ID, map[string]any{"custom_name": "owner control"}, nil); code != http.StatusNoContent {
		t.Fatalf("owner cannot patch runtime fixture: %d", code)
	}

	checked := 0
	err := chi.Walk(NewRouter(s.H).(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") || strings.HasPrefix(route, "/api/tokens") ||
			route == "/api/me" || route == "/api/cli-token" || route == "/api/workspaces" ||
			strings.HasPrefix(route, "/api/invitations") || strings.HasPrefix(route, "/api/daemon") || strings.HasPrefix(route, "/api/role") || strings.HasPrefix(route, "/api/run/coresearch") {
			return nil
		}

		path := route
		header := wb.ID
		if strings.HasPrefix(route, "/api/workspaces/{id}") {
			path = strings.Replace(path, "{id}", wa.ID, 1)
			header = ""
		} else if strings.HasPrefix(route, "/api/connectors") || route == "/api/mcp-servers" || route == "/api/skills" || route == "/api/skills/import" {
			// Connectors and skills are workspace-scoped: probe them with A's
			// workspace, which membership must refuse for B's user.
			header = wa.ID
			path = strings.Replace(path, "{name}", "omim", 1)
		} else if route == "/api/coresearch/uploads" || route == "/api/coresearch/templates" || route == "/api/coresearch/bootstrap" || route == "/api/coresearch/conversations" || route == "/api/coresearch/projects" || route == "/api/analytics" || route == "/api/storage" || route == "/api/autoresearch" || route == "/api/cards" || route == "/api/inbox" || route == "/api/runtimes" || route == "/api/researches" || route == "/api/agents" ||
			route == "/api/attachments" || route == "/api/packs" || route == "/api/teams" || route == "/api/packs/catalog" || route == "/api/me/pack" {
			// Collection routes have no resource id to probe, so scope B to A.
			header = wa.ID
		}
		if route == "/api/inbox/{id}/read" {
			path = strings.Replace(path, "{id}", item, 1)
		} else if strings.HasPrefix(route, "/api/mcp-servers/{id}") {
			path = strings.Replace(path, "{id}", mcp.ID, 1)
		} else if strings.HasPrefix(route, "/api/runtimes/{id}") {
			path = strings.Replace(path, "{id}", runtimeID, 1)
		} else if strings.HasPrefix(route, "/api/researches/{id}") {
			path = strings.Replace(path, "{id}", research.ID, 1)
		} else if strings.HasPrefix(route, "/api/agents/{id}") {
			path = strings.Replace(path, "{id}", agent.ID, 1)
		} else if strings.HasPrefix(route, "/api/agent-runs/{id}") {
			path = strings.Replace(path, "{id}", runID.String(), 1)
		} else {
			for k, v := range ids {
				path = strings.ReplaceAll(path, k, v)
			}
		}
		path = strings.TrimSuffix(path, "/")
		var body any
		switch {
		case strings.HasSuffix(route, "/stop"):
			body = map[string]string{"reason": "outsider"}
		case strings.HasSuffix(route, "/settings"):
			body = map[string]any{"settings": map[string]any{}}

		case route == "/api/coresearch/conversations/{conversation}/turns" || route == "/api/coresearch/conversations":
			body = map[string]string{"body": "outsider", "request_id": uuid.NewString()}
		case route == "/api/coresearch/projects" || route == "/api/coresearch/projects/{project}":
			body = map[string]string{"name": "outsider"}
		case route == "/api/coresearch/projects/{project}/docs/{file}/steps":
			body = map[string]any{"base": 0, "client_id": "outsider", "steps": []any{[]any{0}}}
		case route == "/api/coresearch/projects/{project}/docs/{file}/cursor":
			body = map[string]int{"anchor": 0, "head": 0}
		case route == "/api/coresearch/projects/{project}/members":
			body = map[string]string{"user_id": s.userID(t, b), "role": "member"}
		case strings.HasSuffix(route, "/restore"):
			body = map[string]int{"n": 1, "base": 1}
		case route == "/api/researches" || route == "/api/researches/{id}":
			body = map[string]string{"name": "outsider research"}
		case route == "/api/agents" || route == "/api/agents/{id}":
			body = map[string]string{"name": "outsider agent", "harness": "codex"}
		case route == "/api/cards/{id}/pack":
			body = map[string]string{"pack_id": c.ID}
		case route == "/api/me/pack":
			body = map[string]string{"name": "demo"}
		case route == "/api/cards/{id}/properties":
			body = map[string]any{"research_id": nil}
		case route == "/api/cards":
			body = map[string]any{"question": "outsider card"}
		case route == "/api/cards/{id}/teams":
			body = map[string]string{"reporting": c.ID}
		case route == "/api/cards/{id}" && method == http.MethodPatch:
			body = map[string]string{"title": "outsider edit"}
		case strings.HasSuffix(route, "/comments") && method == http.MethodPost:
			body = map[string]string{"body": "outsider comment"}
		case strings.HasSuffix(route, "/") && method == http.MethodPatch:
			body = map[string]string{"name": "outsider rename"}
		case route == "/api/runtimes/{id}" && method == http.MethodPatch:
			body = map[string]any{"max_concurrent": 2}
		case strings.HasSuffix(route, "/members/{user}") && method == http.MethodPatch:
			body = map[string]string{"role": "member"}
		case route == "/api/workspaces/{id}/invitations" && method == http.MethodPost:
			body = map[string]any{"email": "invite@example.org", "role": "member"}
		}
		code := s.call(t, method, path, b, header, body, nil)
		if code != http.StatusNotFound {
			t.Errorf("%s %s as an outsider returned %d, want 404", method, route, code)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d workspace routes checked", checked)
	}
	t.Logf("checked %d registered workspace routes", checked)

	// A member of B must also be unable to address A's card or inbox item while
	// supplying B's workspace scope. These checks catch ID-only lookups.
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/cards/" + c.ID, nil},
		{http.MethodGet, "/api/cards/" + c.ID + "/comments", nil},
		{http.MethodPost, "/api/cards/" + c.ID + "/comments", map[string]string{"body": "probe"}},
		{http.MethodGet, "/api/cards/" + c.ID + "/events", nil},
		{http.MethodGet, "/api/cards/" + c.ID + "/attachments/" + attached, nil},
		{http.MethodPost, "/api/inbox/" + item + "/read", nil},
	} {
		if code := s.call(t, route.method, route.path, b, wb.ID, route.body, nil); code != http.StatusNotFound {
			t.Errorf("%s %s under B returned %d, want 404", route.method, route.path, code)
		}
	}

	var cards []card
	s.call(t, "GET", "/api/cards", a, wa.ID, nil, &cards)
	if len(cards) != 1 {
		t.Fatalf("an outsider changed workspace A: %d cards", len(cards))
	}
	var comments int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM comment WHERE workspace_id = $1 AND card_id = $2`, wa.ID, c.ID).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if comments != 0 {
		t.Fatalf("an outsider added %d comments to workspace A", comments)
	}
	if code := s.call(t, http.MethodPost, "/api/agent-runs/"+runID.String()+"/cancel", b, wb.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("outsider cancelled A's real agent run: %d", code)
	}
	var statusAfter string
	var attemptAfter int32
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status, attempt FROM agent_run WHERE workspace_id = $1 AND id = $2`, wa.ID, runID).Scan(&statusAfter, &attemptAfter); err != nil {
		t.Fatal(err)
	}
	if statusAfter != originalStatus || attemptAfter != originalAttempt {
		t.Fatalf("outsider changed agent run: status=%s attempt=%d, want %s/%d", statusAfter, attemptAfter, originalStatus, originalAttempt)
	}
	if code := s.call(t, http.MethodGet, "/api/agent-runs/"+runID.String(), a, wa.ID, nil, &ownRun); code != http.StatusOK || ownRun.Status != originalStatus {
		t.Fatalf("owner cannot read unchanged agent run: %d %+v", code, ownRun)
	}
	if code := s.call(t, http.MethodDelete, "/api/runtimes/"+runtimeID, a, wa.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("owner cannot delete runtime fixture: %d", code)
	}
}
