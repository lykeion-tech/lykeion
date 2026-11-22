package slack

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	botTok = "xoxb-111-secret-bot"
	appTok = "xapp-1-A0APP1-222-secret-app"
)

// fakeSlack answers the two calls Verify makes.
func fakeSlack(t *testing.T) *Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		auth := r.Header.Get("Authorization")
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth.test"):
			if r.Form.Get("token") != botTok && auth != "Bearer "+botTok {
				w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
				return
			}
			w.Write([]byte(`{"ok":true,"team_id":"T1","team":"Lab","user_id":"UBOT","bot_id":"B1"}`))
		case strings.HasSuffix(r.URL.Path, "/apps.connections.open"):
			if auth != "Bearer "+appTok {
				w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
				return
			}
			w.Write([]byte(`{"ok":true,"url":"wss://x"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Service{APIURL: srv.URL}
}

func TestVerify(t *testing.T) {
	s := fakeSlack(t)
	id, err := s.Verify(t.Context(), botTok, appTok)
	if err != nil || id.TeamID != "T1" || id.TeamName != "Lab" || id.BotUserID != "UBOT" || id.AppID != "A0APP1" {
		t.Fatalf("%+v %v", id, err)
	}

	_, err = s.Verify(t.Context(), "xoxb-bad-token", appTok)
	if err == nil || err.Error() != "Slack rejected the bot token" {
		t.Fatalf("bad bot: %v", err)
	}
	_, err = s.Verify(t.Context(), botTok, "xapp-1-A0APP1-bad")
	if err == nil || err.Error() != "Slack rejected the app token" {
		t.Fatalf("bad app: %v", err)
	}
	for _, c := range [][2]string{{"nope", appTok}, {botTok, "nope"}} {
		if _, err := s.Verify(t.Context(), c[0], c[1]); err == nil {
			t.Fatalf("accepted %v", c)
		}
	}
	for _, tok := range []string{"xoxb-bad-token", "xapp-1-A0APP1-bad"} {
		if _, err := s.Verify(t.Context(), tok, tok); err != nil && strings.Contains(err.Error(), tok) {
			t.Fatalf("error leaks token: %v", err)
		}
	}
}

func TestScrub(t *testing.T) {
	err := scrub(errors.New("post https://x?token=xoxb-1-abc failed"), "xoxb-1-abc", "")
	if strings.Contains(err.Error(), "xoxb-1-abc") {
		t.Fatal(err)
	}
	if scrub(nil, "a") != nil {
		t.Fatal("nil must stay nil")
	}
}
