package slack

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type linkFix struct {
	Env  *testutil.Env
	S    *Service
	WS   uuid.UUID
	User uuid.UUID
	Inst db.SlackInstallation
}

// linkSetup seeds a workspace with a member and a Slack installation, and a
// fake users.info: U1 has the member's email (upper-cased), U2 another email,
// U3 the member's email unconfirmed.
func linkSetup(t *testing.T) *linkFix {
	t.Helper()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if !strings.HasSuffix(r.URL.Path, "/users.info") {
			http.NotFound(w, r)
			return
		}
		switch r.Form.Get("user") {
		case "U1":
			w.Write([]byte(`{"ok":true,"user":{"id":"U1","is_email_confirmed":true,"profile":{"email":"OWNER@lab.org"}}}`))
		case "U2":
			w.Write([]byte(`{"ok":true,"user":{"id":"U2","is_email_confirmed":true,"profile":{"email":"other@lab.org"}}}`))
		case "U3":
			w.Write([]byte(`{"ok":true,"user":{"id":"U3","is_email_confirmed":false,"profile":{"email":"owner@lab.org"}}}`))
		default:
			w.Write([]byte(`{"ok":false,"error":"user_not_found"}`))
		}
	}))
	t.Cleanup(api.Close)
	f := &linkFix{Env: env, S: &Service{Store: st, Key: bytes.Repeat([]byte{9}, 32), APIURL: api.URL, PublicURL: "https://lyk.test"}}
	ctx := context.Background()
	u, err := st.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	f.User, f.WS = u.ID, f.newWorkspace(t, u.ID, "a")
	f.Inst = f.install(t, f.WS, u.ID, "A1")
	return f
}

func (f *linkFix) newWorkspace(t *testing.T, user uuid.UUID, slug string) uuid.UUID {
	t.Helper()
	ctx, ws := context.Background(), uuid.New()
	err := f.S.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: ws, Slug: slug + ws.String()[:8], Name: "w"}); err != nil {
			return err
		}
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws, UserID: user, Role: "owner"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func (f *linkFix) install(t *testing.T, ws, user uuid.UUID, app string) db.SlackInstallation {
	t.Helper()
	ctx, id := context.Background(), uuid.New()
	var inst db.SlackInstallation
	err := f.S.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		bot, err := f.S.Seal(ws, id, "bot", "xoxb-1-bot")
		if err != nil {
			return err
		}
		appTok, err := f.S.Seal(ws, id, "app", "xapp-1-"+app+"-2-app")
		if err != nil {
			return err
		}
		inst, err = tx.Q.CreateSlackInstallation(ctx, db.CreateSlackInstallationParams{ID: id, AppID: app, TeamID: "T1", TeamName: "Lab",
			BotUserID: "UBOT", BotTokenCiphertext: bot, AppTokenCiphertext: appTok, InstalledBy: user})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

func (f *linkFix) mint(t *testing.T, ws uuid.UUID, inst db.SlackInstallation, slackUser string) string {
	t.Helper()
	var url string
	err := f.S.Store.InWorkspace(context.Background(), ws, f.User, func(tx *store.Tx) (err error) {
		url, err = f.S.MintLink(context.Background(), tx, inst, slackUser)
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://lyk.test/app/slack/link?workspace=" + ws.String() + "&token="
	if !strings.HasPrefix(url, want) || len(url) == len(want) {
		t.Fatalf("url: %s", url)
	}
	return strings.TrimPrefix(url, want)
}

func (f *linkFix) mine(t *testing.T) int {
	t.Helper()
	var n int
	_ = f.S.Store.InWorkspace(context.Background(), f.WS, f.User, func(tx *store.Tx) error {
		b, err := tx.Q.ListMySlackBindings(context.Background())
		n = len(b)
		return err
	})
	return n
}

func TestRedeemLinks(t *testing.T) {
	f := linkSetup(t)
	raw := f.mint(t, f.WS, f.Inst, "U1")
	got, err := f.S.Redeem(context.Background(), f.WS, f.User, "owner@lab.org", raw)
	if err != nil || got != "U1" || f.mine(t) != 1 {
		t.Fatalf("%q %v bindings=%d", got, err, f.mine(t))
	}
}

func TestRedeemEmailMismatch(t *testing.T) {
	f := linkSetup(t)
	for _, u := range []string{"U2", "U3"} { // other email; unconfirmed email
		raw := f.mint(t, f.WS, f.Inst, u)
		_, err := f.S.Redeem(context.Background(), f.WS, f.User, "owner@lab.org", raw)
		if !errors.Is(err, ErrEmailMismatch) || err.Error() != "Your Slack email doesn't match your Lykeion email." {
			t.Fatalf("%s: %v", u, err)
		}
		// the token is spent either way
		if _, err := f.S.Redeem(context.Background(), f.WS, f.User, "other@lab.org", raw); !errors.Is(err, ErrLinkExpired) {
			t.Fatalf("%s reuse: %v", u, err)
		}
	}
	if f.mine(t) != 0 {
		t.Fatal("bound")
	}
}

func TestRedeemTokenSingleUse(t *testing.T) {
	f := linkSetup(t)
	raw := f.mint(t, f.WS, f.Inst, "U1")
	if _, err := f.S.Redeem(context.Background(), f.WS, f.User, "owner@lab.org", raw); err != nil {
		t.Fatal(err)
	}
	_, err := f.S.Redeem(context.Background(), f.WS, f.User, "owner@lab.org", raw)
	if !errors.Is(err, ErrLinkExpired) || err.Error() != "This link has expired. Message Aristotle in Slack for a new one." {
		t.Fatalf("%v", err)
	}
}

func TestRedeemOtherWorkspace(t *testing.T) {
	f := linkSetup(t)
	other := f.newWorkspace(t, f.User, "b")
	raw := f.mint(t, f.WS, f.Inst, "U1")
	if _, err := f.S.Redeem(context.Background(), other, f.User, "owner@lab.org", raw); !errors.Is(err, ErrLinkExpired) {
		t.Fatalf("%v", err)
	}
	if _, err := f.S.Redeem(context.Background(), f.WS, f.User, "owner@lab.org", raw); err != nil {
		t.Fatalf("token wrongly spent by the other workspace: %v", err)
	}
}
