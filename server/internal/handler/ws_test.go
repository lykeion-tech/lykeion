package handler

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/store"
)

type wsMsg struct {
	Type  string
	Error string
	Event struct {
		ID   int64
		Type string
	}
}

func dial(t *testing.T, s *server, tok, wsID string, cursor int64) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/ws",
		http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.WriteJSON(map[string]any{"type": "subscribe", "workspace": wsID, "cursor": cursor}); err != nil {
		t.Fatal(err)
	}
	return c
}

func next(t *testing.T, c *websocket.Conn, within time.Duration) (wsMsg, bool) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(within))
	var m wsMsg
	if err := c.ReadJSON(&m); err != nil {
		return m, false
	}
	return m, true
}

func TestReplayThenLive(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	conn := dial(t, s, a, w.ID, 0)
	m, ok := next(t, conn, 2*time.Second)
	if !ok || m.Event.Type != "workspace.created" {
		t.Fatalf("replay: %+v", m)
	}
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, nil)
	m, ok = next(t, conn, 1500*time.Millisecond) // faster than the 2 s poll: the Redis wake-up worked
	if !ok || m.Event.Type != "card.created" {
		t.Fatalf("live: %+v", m)
	}
}

func TestReconnectMissesNothingUnderConcurrentWrites(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, nil)
		}()
	}
	wg.Wait()
	conn := dial(t, s, a, w.ID, 0)
	seen := map[int64]bool{}
	var last int64
	for len(seen) < 21 {
		m, ok := next(t, conn, 3*time.Second)
		if !ok {
			t.Fatalf("received %d of 21 events", len(seen))
		}
		if seen[m.Event.ID] || m.Event.ID <= last {
			t.Fatalf("event %d duplicated or out of order", m.Event.ID)
		}
		seen[m.Event.ID], last = true, m.Event.ID
	}
	conn.Close()
	again := dial(t, s, a, w.ID, last)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "after"}, nil)
	if m, ok := next(t, again, 3*time.Second); !ok || m.Event.ID <= last {
		t.Fatalf("after reconnect: %+v", m)
	}
}

func TestNonMemberRefusedAndRemovedMemberCutOff(t *testing.T) {
	s := newServer(t)
	a, b, x := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org"), s.signIn(t, "x@else.org")
	w := s.workspace(t, a, "lab")
	if m, _ := next(t, dial(t, s, x, w.ID, 0), 2*time.Second); m.Type != "error" {
		t.Fatalf("stranger subscribed: %+v", m)
	}
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil)
	conn := dial(t, s, b, w.ID, 1<<62)
	s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/members/"+s.userID(t, b), a, "", nil, nil)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "secret"}, nil)
	for {
		m, ok := next(t, conn, 3*time.Second)
		if !ok {
			return // closed: good
		}
		if m.Event.Type == "card.created" {
			t.Fatal("a removed member received a new card")
		}
	}
}

func TestWithoutRedisPollingStillDelivers(t *testing.T) {
	s := newServer(t)
	s.H.Hub.Redis = nil
	s.H.Hub.Poll = 300 * time.Millisecond
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	conn := dial(t, s, a, w.ID, 0)
	next(t, conn, 2*time.Second)
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, nil)
	if m, ok := next(t, conn, 2*time.Second); !ok || m.Event.Type != "card.created" {
		t.Fatalf("%+v", m)
	}
}

// A full replay crosses page boundaries and still accepts concurrent live inserts.
func TestReplayPagesAndConcurrentLive(t *testing.T) {
	s := newServer(t)
	s.H.Hub.Poll = 50 * time.Millisecond
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	wsID := uuid.MustParse(w.ID)
	u := uuid.MustParse(s.userID(t, a))
	emit := func(n int) error {
		return s.H.Store.InWorkspace(t.Context(), wsID, u, func(tx *store.Tx) error {
			for i := 0; i < n; i++ {
				if err := tx.Emit(t.Context(), "test.replay", nil, store.User(u), nil); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := emit(1100); err != nil {
		t.Fatal(err)
	}
	c := dial(t, s, a, w.ID, 0)
	done := make(chan error, 1)
	go func() { done <- emit(50) }()
	var last int64
	for i := 0; i < 1151; i++ {
		m, ok := next(t, c, 3*time.Second)
		if !ok || m.Type != "event" || m.Event.ID <= last {
			t.Fatalf("event %d after %d: %+v ok=%v", i, last, m, ok)
		}
		last = m.Event.ID
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRedisOutagePollingStillDelivers(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	// A reachable endpoint which never acknowledges Redis commands catches an
	// unbounded subscription wait, unlike a nil client or connection refusal.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	rd := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxRetries: -1})
	defer rd.Close()
	s.H.Hub.Redis = rd
	s.H.Hub.Poll = 50 * time.Millisecond
	// The database commit is independent of failed wake-up publication.
	s.H.Store.Publish = nil
	c := dial(t, s, a, w.ID, 0)
	if m, ok := next(t, c, time.Second); !ok || m.Event.Type != "workspace.created" {
		t.Fatalf("outage replay: %+v %v", m, ok)
	}
	select {
	case peer := <-accepted:
		defer peer.Close()
	case <-time.After(time.Second):
		t.Fatal("Redis subscription was not attempted")
	}
	if status := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "poll during outage"}, nil); status != 201 {
		t.Fatal(status)
	}
	if m, ok := next(t, c, time.Second); !ok || m.Event.Type != "card.created" {
		t.Fatalf("outage live: %+v %v", m, ok)
	}
}

func TestWSOriginAndUnauthenticatedRejected(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	for _, head := range []http.Header{{}, {"Authorization": {"Bearer " + a}, "Origin": {"https://else.org"}}} {
		c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/ws", head)
		if c != nil {
			c.Close()
			t.Fatal("unauthorized upgrade succeeded")
		}
		if err == nil || resp == nil || (resp.StatusCode != 401 && resp.StatusCode != 403) {
			t.Fatalf("rejection: %v %+v", err, resp)
		}
	}
}

// Holding a member row lock must stop a replay while removal is uncommitted;
// once the deletion commits the socket must close without sending that replay.
func TestWSRemovalCommitPreventsReplay(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil)
	bid := s.userID(t, b)
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", w.ID, bid); err != nil {
		t.Fatal(err)
	}
	c := dial(t, s, b, w.ID, 0)
	// Wait until the replay is waiting on the row lock before committing removal.
	deadline := time.Now().Add(time.Second)
	for {
		var blocked bool
		err = s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename='lykeion_app' AND wait_event_type='Lock')").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replay did not wait for membership removal")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	m, ok := next(t, c, time.Second)
	if !ok || m.Type != "error" || m.Error != "workspace not found" {
		t.Fatalf("removed replay: %+v %v", m, ok)
	}
	if m, ok := next(t, c, time.Second); ok {
		t.Fatalf("socket stayed open: %+v", m)
	}
}

func TestWSCloseStopsClientsAndRejectsNewSockets(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := dial(t, s, a, w.ID, 0)
	if _, ok := next(t, c, time.Second); !ok {
		t.Fatal("initial replay missing")
	}
	closer, ok := any(s.H.Hub).(interface{ Close() error })
	if !ok {
		t.Fatal("hub has no lifecycle close")
	}
	done := make(chan error, 1)
	go func() { done <- closer.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("hub close did not finish")
	}
	if m, ok := next(t, c, time.Second); ok {
		t.Fatalf("socket stayed open: %+v", m)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := dial(t, s, a, w.ID, 0)
	if m, ok := next(t, c2, time.Second); ok {
		t.Fatalf("closed hub sent %+v", m)
	}
}

// A non-reading peer forces a write to block. Its member lock must remain held
// until the flush's fixed deadline, then removal can complete promptly.
func TestWSBlockedFlushKeepsMemberLockAndBoundsRemoval(t *testing.T) {
	s := newServer(t)
	s.H.Hub.Redis = nil
	s.H.Hub.Poll = 50 * time.Millisecond
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil)
	wsID, u := uuid.MustParse(w.ID), uuid.MustParse(s.userID(t, a))
	bid := s.userID(t, b)
	// A frame larger than the TCP buffers reliably creates backpressure.
	if err := s.H.Store.InWorkspace(t.Context(), wsID, u, func(tx *store.Tx) error {
		return tx.Emit(t.Context(), "test.large", nil, store.User(u), map[string]string{"data": strings.Repeat("x", 16<<20)})
	}); err != nil {
		t.Fatal(err)
	}
	_ = dial(t, s, b, w.ID, 0)
	deadline := time.Now().Add(time.Second)
	for {
		var locked bool
		err := s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename='lykeion_app' AND state='idle in transaction' AND query LIKE '%ORDER BY id LIMIT%')").Scan(&locked)
		if err != nil {
			t.Fatal(err)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no flush transaction was held while writing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SET LOCAL lock_timeout='150ms'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", w.ID, bid); err == nil {
		t.Fatal("membership lock was released before the websocket write")
	}
	tx.Rollback(context.Background())
	start := time.Now()
	if status := s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/members/"+bid, a, "", nil, nil); status != 204 {
		t.Fatal(status)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("slow peer delayed removal for %s", elapsed)
	}
}
