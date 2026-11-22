package slack

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"lykeion/server/internal/store"
)

// holds counts live stub holders per installation.
type holds struct {
	mu sync.Mutex
	n  map[uuid.UUID]int
}

func (h *holds) add(id uuid.UUID, d int) { h.mu.Lock(); h.n[id] += d; h.mu.Unlock() }
func (h *holds) get(id uuid.UUID) int    { h.mu.Lock(); defer h.mu.Unlock(); return h.n[id] }

func (h *holds) hold(ctx context.Context, _, id uuid.UUID) {
	h.add(id, 1)
	<-ctx.Done()
	h.add(id, -1)
}

func superSetup(t *testing.T) (*linkFix, *holds, func() *supervisor) {
	f := linkSetup(t)
	h := &holds{n: map[uuid.UUID]int{}}
	return f, h, func() *supervisor {
		s := &Service{Store: f.S.Store, Key: f.S.Key, Redis: f.Env.Redis, LeaseTTL: 300 * time.Millisecond, holdFunc: h.hold}
		sv := s.supervisor()
		t.Cleanup(sv.stop)
		return sv
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal(what)
}

func leaseOwner(f *linkFix) string {
	return f.Env.Redis.Get(context.Background(), "lyk:slack:lease:"+f.Inst.ID.String()).Val()
}

func TestLeaseSingleHolder(t *testing.T) {
	f, h, newSV := superSetup(t)
	ctx := context.Background()
	a, b := newSV(), newSV()
	for range 2 {
		a.tick(ctx)
		b.tick(ctx)
	}
	eventually(t, "one holder", func() bool { return h.get(f.Inst.ID) == 1 })
	if len(a.held)+len(b.held) != 1 {
		t.Fatalf("held: a=%d b=%d", len(a.held), len(b.held))
	}
	owner := a
	if len(b.held) == 1 {
		owner = b
	}
	if leaseOwner(f) != owner.id {
		t.Fatalf("lease owner %q, want %q", leaseOwner(f), owner.id)
	}
	time.Sleep(100 * time.Millisecond)
	if n := h.get(f.Inst.ID); n != 1 {
		t.Fatalf("holders: %d", n)
	}
}

func TestLeaseTakeover(t *testing.T) {
	f, h, newSV := superSetup(t)
	ctx := context.Background()
	a, b := newSV(), newSV()
	a.tick(ctx)
	eventually(t, "a holds", func() bool { return h.get(f.Inst.ID) == 1 })
	b.tick(ctx)
	if len(b.held) != 0 {
		t.Fatal("b took a live lease")
	}
	// a stops renewing (a crashed instance); b takes over once the lease lapses.
	eventually(t, "b takes over", func() bool { b.tick(ctx); return len(b.held) == 1 })
	if leaseOwner(f) != b.id {
		t.Fatalf("lease owner %q", leaseOwner(f))
	}
	// a comes back: its renewal fails and its holder stops.
	a.tick(ctx)
	if len(a.held) != 0 {
		t.Fatal("a still holds")
	}
	eventually(t, "only b holds", func() bool { return h.get(f.Inst.ID) == 1 })
}

func TestRevokedStopsHolding(t *testing.T) {
	f, h, newSV := superSetup(t)
	ctx := context.Background()
	a := newSV()
	a.tick(ctx)
	eventually(t, "a holds", func() bool { return h.get(f.Inst.ID) == 1 })

	if err := a.s.revoke(ctx, f.Inst); err != nil {
		t.Fatal(err)
	}
	var status string
	err := f.S.Store.InWorkspace(ctx, f.WS, f.User, func(tx *store.Tx) error {
		inst, err := tx.Q.GetSlackInstallationByID(ctx, f.Inst.ID)
		status = inst.Status
		return err
	})
	if err != nil || status != "revoked" {
		t.Fatalf("status %q, %v", status, err)
	}
	var events int
	if err := f.Env.Owner.QueryRow(ctx, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='slack.changed'`, f.WS).Scan(&events); err != nil || events != 1 {
		t.Fatalf("slack.changed events: %d, %v", events, err)
	}

	a.tick(ctx)
	eventually(t, "holder stopped", func() bool { return h.get(f.Inst.ID) == 0 })
	if len(a.held) != 0 || leaseOwner(f) != "" {
		t.Fatalf("still held: %d, lease %q", len(a.held), leaseOwner(f))
	}
}

func parseEvent(t *testing.T, raw string) slackevents.EventsAPIEvent {
	t.Helper()
	e, err := slackevents.ParseEvent(json.RawMessage(raw), slackevents.OptionNoVerifyToken())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func callback(event string) string {
	return `{"type":"event_callback","team_id":"T1","api_app_id":"A1","event_id":"Ev1","event":` + event + `}`
}

func TestToMessage(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  Message
		ok    bool
	}{
		{"mention", `{"type":"app_mention","user":"U1","text":"<@UBOT> hi","ts":"1.2","thread_ts":"1.1","channel":"C1"}`,
			Message{EventID: "Ev1", AppID: "A1", User: "U1", Channel: "C1", ChannelType: "channel", TS: "1.2", ThreadTS: "1.1", Text: "<@UBOT> hi"}, true},
		{"edited mention", `{"type":"app_mention","user":"U1","text":"x","ts":"1.2","channel":"C1","edited":{"user":"U1","ts":"1.3"}}`,
			Message{EventID: "Ev1", AppID: "A1", User: "U1", Channel: "C1", ChannelType: "channel", TS: "1.2", Text: "x", Subtype: "message_changed"}, true},
		{"dm with file", `{"type":"message","user":"U1","text":"look","ts":"2.1","channel":"D1","channel_type":"im","files":[{"id":"F1"}]}`,
			Message{EventID: "Ev1", AppID: "A1", User: "U1", Channel: "D1", ChannelType: "im", TS: "2.1", Text: "look", HasFiles: true}, true},
		{"dm from bot", `{"type":"message","subtype":"bot_message","bot_id":"B1","text":"x","ts":"2.1","channel":"D1","channel_type":"im"}`,
			Message{EventID: "Ev1", AppID: "A1", Channel: "D1", ChannelType: "im", TS: "2.1", Text: "x", BotID: "B1", Subtype: "bot_message"}, true},
		{"channel message", `{"type":"message","user":"U1","text":"x","ts":"2.1","channel":"C1","channel_type":"channel"}`, Message{}, false},
		{"other event", `{"type":"reaction_added","user":"U1","reaction":"eyes"}`, Message{}, false},
	}
	for _, c := range cases {
		got, ok := toMessage(parseEvent(t, callback(c.event)))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestRevocationEvent(t *testing.T) {
	for typ, want := range map[string]bool{"app_uninstalled": true, "tokens_revoked": true, "app_mention": false} {
		if got := revokes(parseEvent(t, callback(`{"type":"`+typ+`"}`))); got != want {
			t.Errorf("%s: %v", typ, got)
		}
	}
}

func TestToCommand(t *testing.T) {
	var sc slackgo.SlashCommand
	raw := `{"command":"/study","text":"sleep and memory","user_id":"U1","channel_id":"D1","response_url":"https://hooks.slack.test/r/1","api_app_id":"A1"}`
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		t.Fatal(err)
	}
	want := Command{Name: "/study", Text: "sleep and memory", User: "U1", Channel: "D1", ResponseURL: "https://hooks.slack.test/r/1"}
	if got := toCommand(sc); got != want {
		t.Fatalf("%+v", got)
	}
}

func TestToAction(t *testing.T) {
	var ic slackgo.InteractionCallback
	raw := `{"type":"block_actions","user":{"id":"U1"},"channel":{"id":"D1"},"response_url":"https://hooks.slack.test/r/2",
		"actions":[{"action_id":"` + ClearConfirmAction + `","block_id":"b","type":"button","value":"clear"}]}`
	if err := json.Unmarshal([]byte(raw), &ic); err != nil {
		t.Fatal(err)
	}
	got, ok := toAction(ic)
	want := Action{ID: ClearConfirmAction, User: "U1", Channel: "D1", ResponseURL: "https://hooks.slack.test/r/2"}
	if !ok || got != want {
		t.Fatalf("%+v %v", got, ok)
	}
	var other slackgo.InteractionCallback
	_ = json.Unmarshal([]byte(`{"type":"view_submission","user":{"id":"U1"}}`), &other)
	if _, ok := toAction(other); ok {
		t.Fatal("non-block action mapped")
	}
}
