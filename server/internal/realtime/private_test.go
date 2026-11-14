package realtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func TestPrivateEventWakeupsAndReplay(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, user, other := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'replay@test'),($2,'hidden@test')`, []any{user, other}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'replay','replay')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'member')`, []any{ws, user, other}},
		{`INSERT INTO event(workspace_id,type,actor_type) VALUES($1,'first','system')`, []any{ws}},
		{`INSERT INTO event(workspace_id,type,actor_type,recipient_id) SELECT $1,'hidden','system',$2 FROM generate_series(1,1001)`, []any{ws, other}},
		{`INSERT INTO event(workspace_id,type,actor_type) VALUES($1,'last','system')`, []any{ws}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	h := &Hub{Store: &store.Store{Pool: env.App}, Redis: env.Redis}
	shared := env.Redis.Subscribe(ctx, channel(ws))
	defer shared.Close()
	if _, err := shared.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	private := env.Redis.Subscribe(ctx, recipientChannel(ws, user))
	defer private.Close()
	if _, err := private.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	h.Publish(ctx, []db.Event{{ID: 77, WorkspaceID: ws, RecipientID: &user, Type: "manual.created"}})
	if _, err := private.ReceiveMessage(ctx); err != nil {
		t.Fatal(err)
	}
	if msg, err := shared.ReceiveTimeout(ctx, 50*time.Millisecond); err == nil {
		t.Fatalf("private identity leaked to shared channel: %+v", msg)
	}
	// Replay uses only the database, including after a reconnect cursor.
	h.Redis = nil
	cursor := int64(0)
	for _, want := range [][]string{{"first", "last"}, {}} {
		done := make(chan error, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				done <- err
				return
			}
			defer c.Close()
			_, err = h.flush(ctx, c, ws, user, &cursor)
			done <- err
		}))
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for {
			var msg serverMsg
			if err := c.ReadJSON(&msg); err != nil {
				break
			}
			got = append(got, msg.Event.Type)
		}
		c.Close()
		srv.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("replay=%v, want %v", got, want)
		}
	}
	if cursor == 0 {
		t.Fatal("cursor did not advance")
	}
}
