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

func TestSnapshotThenConcurrentCommitReplaysOnOtherInstance(t *testing.T) {
	api, db, user, workspace, token, csrf := fixture(t)
	first := httptest.NewServer(api.Handler())
	defer first.Close()
	other, err := New(db, api.Key, []string{"http://127.0.0.1:1430"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	second := httptest.NewServer(other.Handler())
	defer second.Close()
	base := first.URL + "/v1/workspaces/" + workspace
	status, raw := request(t, first.Client(), "GET", base+"/members", token, "", "", nil)
	if status != 200 {
		t.Fatalf("snapshot %d", status)
	}
	var snapshot struct {
		Cursor string `json:"event_cursor"`
	}
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	// Commit in the interval after HTTP snapshot but before the socket subscribes.
	status, raw = request(t, first.Client(), "POST", base+"/commands/preference.update", token, csrf, "snapshot-race", preference(workspace, "dark"))
	if status != 202 {
		t.Fatalf("commit %d %s", status, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(second.URL, "http")+"/v1/workspaces/"+workspace+"/realtime", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://127.0.0.1:1430"}, "Cookie": []string{"lykeion_session=" + token}}, Subprotocols: []string{"lykeion.workspace.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	raw, _ = json.Marshal(map[string]any{"schema_version": "1", "type": "subscribe", "message_id": store.ID("msg"), "payload": map[string]any{"channels": []string{"workspace"}, "subject_refs": []any{}, "after_cursor": snapshot.Cursor}})
	if err = ws.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	if _, raw, err = ws.Read(ctx); err != nil || !bytes.Contains(raw, []byte(`"type":"subscribed"`)) {
		t.Fatalf("subscribe %s %v", raw, err)
	}
	if _, raw, err = ws.Read(ctx); err != nil || !bytes.Contains(raw, []byte(`preference.changed`)) {
		t.Fatalf("lost snapshot gap %s %v", raw, err)
	}
	var delivered struct {
		Cursor string `json:"cursor"`
	}
	if err = json.Unmarshal(raw, &delivered); err != nil {
		t.Fatal(err)
	}
	status, raw = request(t, second.Client(), "GET", second.URL+"/v1/workspaces/"+workspace+"/events?after="+delivered.Cursor, token, "", "", nil)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var replay struct {
		Items []any `json:"items"`
	}
	if err = json.Unmarshal(raw, &replay); err != nil || len(replay.Items) != 0 {
		t.Fatal("delivered event replayed after its cursor", string(raw), err)
	}
	// Cursor authority is bound to this account, not transferable to another.
	stranger, _, code, err := db.Provision(ctx, "Other member", "Other workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := db.Exchange(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, "INSERT INTO memberships(id,workspace_id,user_id,access_role) VALUES($1,$2,$3,'member')", store.ID("mem"), workspace, stranger); err != nil {
		t.Fatal(err)
	}
	status, _ = request(t, second.Client(), "GET", second.URL+"/v1/workspaces/"+workspace+"/events?after="+delivered.Cursor, otherToken, "", "", nil)
	if status != 409 {
		t.Fatal("another member used private cursor", status)
	}
	_ = user
}
