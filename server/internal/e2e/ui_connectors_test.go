//go:build unix

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

type uiDoorBearer string

func (b uiDoorBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// Only test controls live here. Product CRUD, claims, broker and recorded calls
// retain their actual handlers and authorization.
func uiConnectorFixtures(t *testing.T, mux *http.ServeMux, h *handler.H, base string, authorized func(http.ResponseWriter, *http.Request) bool) {
	t.Helper()
	remote := mcp.NewServer(&mcp.Implementation{Name: "ui-remote", Version: "1"}, nil)
	mcp.AddTool(remote, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "remote answer " + fmt.Sprint(in["q"])}}}, nil, nil
	})
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{Stateless: true})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer connector-browser-secret" {
			http.Error(w, "fixture credential required", 401)
			return
		}
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	h.Connectors.HTTP = upstream.Client()
	self, err := os.Executable()
	must(t, err)
	t.Setenv("PATH", filepath.Dir(self)+string(os.PathListSeparator)+os.Getenv("PATH"))
	mux.HandleFunc("GET /lab/connector-source", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"url": upstream.URL, "command": filepath.Base(self)})
	})
	mux.HandleFunc("POST /lab/connector-door", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var in struct {
			Workspace uuid.UUID `json:"workspace"`
			Card      uuid.UUID `json:"card"`
			Name      string    `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid fixture input", 400)
			return
		}
		user, err := h.Store.Global().UpsertUserByEmail(ctx, labEmail)
		if err != nil {
			http.Error(w, "fixture user unavailable", 500)
			return
		}
		// Enqueue only the signed-in fixture owner's actual card in its workspace.
		var run db.AgentRun
		err = h.Store.InWorkspace(ctx, in.Workspace, user.ID, func(tx *store.Tx) error {
			var err error
			run, err = h.AgentRuns.Enqueue(ctx, tx, user.ID, service.NewAgentRun{Card: in.Card, Column: "grounding", Round: 1, Role: "agent", Harness: "codex", Prompt: "Connector browser acceptance"})
			return err
		})
		if err != nil {
			http.Error(w, "fixture enqueue failed", 400)
			return
		}
		rts, err := h.AgentRuns.Register(ctx, user.ID, in.Workspace, "connector-"+run.ID.String(), "Connector acceptance", []string{"codex"}, nil, agent.Platform{})
		if err != nil {
			http.Error(w, "fixture registration failed", 500)
			return
		}
		ref := service.RuntimeRef{WorkspaceID: in.Workspace, RuntimeID: rts[0].ID}
		claims, err := h.AgentRuns.Claim(ctx, user.ID, []service.RuntimeRef{ref}, 1)
		if err != nil || len(claims) != 1 || claims[0].AgentRun.ID != run.ID {
			http.Error(w, "fixture claim failed", 500)
			return
		}
		claim := claims[0]
		ref.Attempt = claim.AgentRun.Attempt
		if err = h.AgentRuns.Start(ctx, user.ID, ref, run.ID); err != nil {
			http.Error(w, "fixture start failed", 500)
			return
		}
		defer h.AgentRuns.Cancel(context.WithoutCancel(ctx), in.Workspace, user.ID, run.ID)
		raw, _ := json.Marshal(claim)
		if bytes.Contains(raw, []byte("connector-browser-secret")) || bytes.Contains(raw, []byte(upstream.URL)) {
			http.Error(w, "private configuration leaked in claim", 500)
			return
		}
		dir := t.TempDir()
		door := &daemon.Door{Server: base, RunToken: claim.Token, CallToken: claim.CallToken, WorkspaceConnectors: claim.WorkspaceConnectors, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: dir} }}
		if err = door.Start(); err != nil {
			http.Error(w, "fixture door failed", 500)
			return
		}
		defer door.Close()
		client, err := mcp.NewClient(&mcp.Implementation{Name: "browser-fixture", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: door.URL, HTTPClient: &http.Client{Transport: uiDoorBearer(door.Token)}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			http.Error(w, "fixture door connection failed", 500)
			return
		}
		defer client.Close()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "call_tool", Arguments: map[string]any{"server": in.Name, "tool": "echo", "args": map[string]any{"q": "browser call"}}})
		if err != nil || result.IsError {
			http.Error(w, "fixture recorded call failed", 500)
			return
		}
		answer := ""
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				answer += text.Text
			}
		}
		if !strings.HasPrefix(answer, "call:") {
			http.Error(w, "fixture answer lacked recorded citation", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"answer": answer, "run_id": run.ID, "claim_configuration_private": true})
	})
	mux.HandleFunc("GET /lab/connector-events", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		ws, err := uuid.Parse(r.URL.Query().Get("workspace"))
		if err != nil {
			http.Error(w, "invalid workspace", 400)
			return
		}
		user, err := h.Store.Global().UpsertUserByEmail(r.Context(), labEmail)
		if err != nil {
			http.Error(w, "fixture user unavailable", 500)
			return
		}
		payloads := []json.RawMessage{}
		err = h.Store.InWorkspace(r.Context(), ws, user.ID, func(tx *store.Tx) error {
			if _, err := tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: user.ID}); err != nil {
				return err
			}
			var after int64
			for {
				rows, err := tx.Q.EventsAfter(r.Context(), db.EventsAfterParams{WorkspaceID: ws, After: after, Lim: 500})
				if err != nil {
					return err
				}
				for _, row := range rows {
					after = row.ID
					if strings.HasPrefix(row.Type, "connector.") {
						payloads = append(payloads, row.Payload)
					}
				}
				if len(rows) < 500 {
					return nil
				}
			}

		})
		if err != nil {
			http.Error(w, "fixture events unavailable", 403)
			return
		}
		json.NewEncoder(w).Encode(payloads)
	})
}
