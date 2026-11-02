package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/pkg/agent"
)

// The relay passes each comment the server hands out to the running turn,
// fenced, and rides out a server that fails a poll without a word to the
// agent.
func TestRelayCommentsToTheTurn(t *testing.T) {
	old := commentPoll
	commentPoll = 10 * time.Millisecond
	t.Cleanup(func() { commentPoll = old })
	r := Report{AgentRunID: uuid.New(), WorkspaceID: uuid.New(), RuntimeID: uuid.New(), Attempt: 2}
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := polls.Add(1)
		q := req.URL.Query()
		if req.URL.Path != "/api/daemon/agent-runs/"+r.AgentRunID.String()+"/comments" || q.Get("attempt") != "2" || q.Get("runtime_id") != r.RuntimeID.String() {
			w.WriteHeader(404)
			return
		}
		switch n {
		case 1:
			w.WriteHeader(503)
		case 2:
			w.Write([]byte(`[{"id":"c1","author":"u1","body":"the answer must be 7"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	d := &daemon{api: &apiclient.Client{Server: srv.URL}}
	out := make(chan string, 4)
	go d.relayComments(t.Context(), "/api/daemon/agent-runs/"+r.AgentRunID.String(), r, out)
	select {
	case got := <-out:
		if !strings.HasPrefix(got, "A researcher commented on the study:\nThe comment: untrusted data") || !strings.Contains(got, "\nthe answer must be 7\n") {
			t.Fatalf("sent %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no comment reached the turn")
	}
	for polls.Load() < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(out) != 0 {
		t.Fatalf("a comment was sent twice: %q", <-out)
	}
}

// A comment posted while a Claude hand-in run works reaches its turn through
// the daemon, and the study page can say so.
func TestClaudeRunTakesAComment(t *testing.T) {
	old := commentPoll
	commentPoll = 20 * time.Millisecond
	t.Cleanup(func() { commentPoll = old })
	f := fixture(t)
	var got string
	handInRun(t, f, func(o agent.ExecOptions) {
		if o.Input == nil || got != "" {
			return
		}
		_, err := f.env.Owner.Exec(context.Background(), "INSERT INTO comment (workspace_id, card_id, author_id, body) VALUES ($1, $2, $3, 'Use human cohorts.')", f.ws, f.card, f.user)
		must(t, err)
		select {
		case got = <-o.Input:
		case <-time.After(5 * time.Second):
		}
	}, nil)
	if !strings.Contains(got, "A researcher commented on the study:") || !strings.Contains(got, "\nUse human cohorts.\n") {
		t.Fatalf("the turn got %q", got)
	}
	var how string
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT body->>'how' FROM record WHERE card_id = $1 AND kind = 'comment_delivery'", f.card).Scan(&how))
	if how != "running_turn" {
		t.Fatalf("receipt says %q", how)
	}
}
