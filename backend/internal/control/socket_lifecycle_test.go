package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
)

func TestSocketSessionExpiryPresenceScopeAndReplayOverflow(t *testing.T) {
	for _, scenario := range []string{"expired-session", "foreign-presence", "replay-overflow", "expired-cursor"} {
		t.Run(scenario, func(t *testing.T) {
			api, db, user, workspace, token, _ := fixture(t)
			server := httptest.NewServer(api.Handler())
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			headers := http.Header{"Origin": []string{"http://127.0.0.1:1430"}, "Cookie": []string{"lykeion_session=" + token}}
			ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/workspaces/"+workspace+"/realtime", &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{"lykeion.workspace.v1"}})
			if err != nil {
				t.Fatal(err)
			}
			defer ws.CloseNow()
			send := func(kind string, payload any) {
				raw, _ := json.Marshal(map[string]any{"schema_version": "1", "type": kind, "message_id": store.ID("msg"), "payload": payload})
				if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
					t.Fatal(err)
				}
			}
			cursor := api.signCursor(workspace, user, 1, 0)
			if scenario == "expired-cursor" {
				cursor = "invalid-expired-cursor"
			}
			send("subscribe", map[string]any{"channels": []string{"workspace"}, "subject_refs": []any{}, "after_cursor": cursor})
			_, raw, err := ws.Read(ctx)
			if scenario == "expired-cursor" {
				if err != nil || !bytes.Contains(raw, []byte(`"reason":"cursor_expired"`)) {
					t.Fatalf("expected resync: %s %v", raw, err)
				}
				// A fresh snapshot cursor permits subscription on the same connection.
				send("subscribe", map[string]any{"channels": []string{"workspace"}, "subject_refs": []any{}, "after_cursor": api.signCursor(workspace, user, 1, 0)})
				_, raw, err = ws.Read(ctx)
			}
			if err != nil || !bytes.Contains(raw, []byte(`"type":"subscribed"`)) {
				t.Fatalf("subscribe: %s %v", raw, err)
			}
			switch scenario {
			case "expired-cursor":
				return
			case "expired-session":
				_, err = db.Pool.Exec(ctx, "UPDATE sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE digest=$1", store.Hash(token))
			case "foreign-presence":
				send("presence.update", map[string]any{"subject_ref": ref{Kind: "workspace", ID: "wsp_not_authorized", Revision: "1"}, "state": "viewing"})
			case "replay-overflow":
				// Commit a sequence jump before the next delivery tick. No event may be silently skipped.
				_, err = db.Pool.Exec(ctx, "UPDATE workspaces SET event_sequence=501 WHERE id=$1", workspace)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, raw, err = ws.Read(ctx)
			if scenario == "replay-overflow" {
				if err != nil || !bytes.Contains(raw, []byte(`"reason":"slow_consumer"`)) {
					t.Fatalf("missing bounded resync: %s %v", raw, err)
				}
				_, _, err = ws.Read(ctx)
			}
			if err == nil || ctx.Err() != nil {
				t.Fatalf("stream did not close promptly: %v", err)
			}
		})
	}
}
