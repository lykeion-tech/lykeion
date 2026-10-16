package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/seal"
	"net/http"
	"strings"
	"testing"
	"time"
)

type apView struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Title         string `json:"title"`
	ExecutionMode string `json:"execution_mode"`
	Triggers      []struct {
		ID        string  `json:"id"`
		Kind      string  `json:"kind"`
		Enabled   bool    `json:"enabled"`
		Signed    bool    `json:"signed"`
		NextRunAt *string `json:"next_run_at"`
	} `json:"triggers"`
	Collaborators []string `json:"collaborators"`
	Subscribers   []string `json:"subscribers"`
}

type runView struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	Reason       string  `json:"reason"`
	Source       string  `json:"source"`
	ActingUserID string  `json:"acting_user_id"`
	CardID       *string `json:"card_id"`
	AgentRunID   *string `json:"agent_run_id"`
	Report       string  `json:"report"`
}

// member signs someone in and adds them to the workspace with the role.
func (s *server) member(t *testing.T, owner, wsID, email, role string) string {
	t.Helper()
	tok := s.signIn(t, email)
	inv := s.invite(t, owner, wsID, email, role)
	if code := s.call(t, "POST", "/api/invitations/"+inv+"/accept", tok, "", nil, nil); code != 200 && code != 204 {
		t.Fatalf("accept: %d", code)
	}
	return tok
}

func (s *server) autoresearch(t *testing.T, tok, wsID string, in map[string]any) apView {
	t.Helper()
	var ap apView
	if code := s.call(t, "POST", "/api/autoresearch", tok, wsID, in, &ap); code != 201 {
		t.Fatalf("create autoresearch: %d", code)
	}
	return ap
}

func TestAutoresearchEditRights(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	m := s.member(t, a, w.ID, "m@lab.org", "member")
	d := s.member(t, a, w.ID, "d@lab.org", "admin")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "description": "What changed?", "execution_mode": "create_card"})
	path := "/api/autoresearch/" + ap.ID

	if code := s.call(t, "GET", path, m, w.ID, nil, nil); code != 200 {
		t.Fatalf("a member reads: %d", code)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", path, map[string]any{"title": "x"}},
		{"POST", path + "/run", nil},
		{"POST", path + "/pause", nil},
		{"POST", path + "/triggers", map[string]any{"kind": "schedule", "cron": "@daily", "tz": "UTC"}},
		{"PUT", path + "/collaborators/" + s.userID(t, m), nil},
		{"PUT", path + "/subscribers/" + s.userID(t, a), nil},
		{"DELETE", path, nil},
	} {
		if code := s.call(t, c.method, c.path, m, w.ID, c.body, nil); code != 403 {
			t.Fatalf("a member without rights: %s %s → %d", c.method, c.path, code)
		}
	}
	if code := s.call(t, "PUT", path+"/subscribers/"+s.userID(t, m), m, w.ID, nil, nil); code != 204 {
		t.Fatalf("subscribe oneself: %d", code)
	}

	if code := s.call(t, "PUT", path+"/collaborators/"+s.userID(t, m), a, w.ID, nil, nil); code != 204 {
		t.Fatalf("add a collaborator: %d", code)
	}
	if code := s.call(t, "PATCH", path, m, w.ID, map[string]any{"title": "Weekly digest"}, nil); code != 200 {
		t.Fatalf("a collaborator edits: %d", code)
	}
	var run runView
	if code := s.call(t, "POST", path+"/run", m, w.ID, nil, &run); code != 201 || run.ActingUserID != s.userID(t, m) || run.Source != "manual" || run.CardID == nil {
		t.Fatalf("a collaborator runs: %d %+v", code, run)
	}
	var c card
	s.call(t, "GET", "/api/cards/"+*run.CardID, a, w.ID, nil, &c)
	if c.OwnerID != s.userID(t, m) || c.BoardColumn != "backlog" {
		t.Fatalf("the card belongs to the clicker: %+v", c)
	}
	if code := s.call(t, "POST", path+"/pause", d, w.ID, map[string]any{"reason": "holiday"}, nil); code != 200 {
		t.Fatalf("an admin pauses: %d", code)
	}
	if code := s.call(t, "POST", path+"/run", a, w.ID, nil, nil); code != 409 {
		t.Fatalf("run a paused autoresearch: %d", code)
	}
	var got apView
	s.call(t, "GET", path, m, w.ID, nil, &got)
	if got.Status != "paused" || got.Title != "Weekly digest" || len(got.Collaborators) != 1 || len(got.Subscribers) != 1 {
		t.Fatalf("%+v", got)
	}
	var runs []runView
	if s.call(t, "GET", path+"/runs", m, w.ID, nil, &runs); len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("history: %+v", runs)
	}
}

func TestAutoresearchInputIsChecked(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	for _, bad := range []map[string]any{
		{"title": "x", "execution_mode": "assign_agent"},
		{"title": " ", "execution_mode": "run_only"},
		{"title": "x", "execution_mode": "run_only", "budget_usd": -1},
		{"title": "x", "execution_mode": "run_only", "assignee": "someone"},
		{"title": strings.Repeat("x", 201), "execution_mode": "run_only"},
	} {
		if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "run_only"})
	path := "/api/autoresearch/" + ap.ID
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"execution_mode": "create_card"}, nil); code != 400 {
		t.Fatalf("change the mode: %d", code)
	}
	for _, bad := range []map[string]any{
		{"kind": "manual"},
		{"kind": "schedule", "cron": "@every 1m", "tz": "UTC"},
		{"kind": "schedule", "cron": "0 9 * * *", "tz": "Mars/Olympus"},
		{"kind": "schedule", "cron": "0 9 * * *", "tz": "UTC", "provider": "github"},
		{"kind": "webhook", "cron": "0 9 * * *"},
		{"kind": "webhook", "provider": "gitlab"},
		{"kind": "webhook", "signing_secret": "s"},
		{"kind": "webhook", "filters": []map[string]any{{"event": ""}}},
		{"kind": "webhook", "filters": []map[string]any{{"event": "push", "actions": []string{" "}}}},
	} {
		if code := s.call(t, "POST", path+"/triggers", a, w.ID, bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
}

func TestTriggerResponsesNeverShowSecrets(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card"})
	path := "/api/autoresearch/" + ap.ID
	var created struct {
		Trigger struct{ ID string }
		Token   string
		URL     string
	}
	if code := s.call(t, "POST", path+"/triggers", a, w.ID, map[string]any{"kind": "webhook", "provider": "github",
		"signing_secret": "whsec-do-not-leak"}, &created); code != 201 || !strings.HasPrefix(created.Token, "lwh_") ||
		created.URL != "/api/webhooks/autoresearch/"+created.Token {
		t.Fatalf("%d %+v", code, created)
	}
	req, err := http.NewRequest("GET", s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a)
	req.Header.Set("X-Workspace-ID", w.ID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{created.Token, "whsec-do-not-leak", "token_hash", "signing_secret"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Fatalf("the autoresearch view shows %q: %s", leak, raw)
		}
	}
	var got apView
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Triggers) != 1 || !got.Triggers[0].Signed {
		t.Fatalf("%s", raw)
	}
}

func TestResumingFiresNothingMissedWhilePaused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card"})
	path := "/api/autoresearch/" + ap.ID
	var created struct{ Trigger struct{ ID string } }
	s.call(t, "POST", path+"/triggers", a, w.ID, map[string]any{"kind": "schedule", "cron": "0 * * * *", "tz": "UTC"}, &created)
	s.call(t, "POST", path+"/pause", a, w.ID, nil, nil)
	ctx := context.Background()
	if _, err := s.Env.Owner.Exec(ctx, `UPDATE autoresearch_trigger SET next_run_at = now() - interval '7 days' WHERE id = $1`, created.Trigger.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Autoresearch.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	s.call(t, "POST", path+"/resume", a, w.ID, nil, nil)
	if err := s.H.Autoresearch.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var runs []runView
	s.call(t, "GET", path+"/runs", a, w.ID, nil, &runs)
	var future bool
	if err := s.Env.Owner.QueryRow(ctx, `SELECT next_run_at > now() FROM autoresearch_trigger WHERE id = $1`, created.Trigger.ID).Scan(&future); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 || !future {
		t.Fatalf("runs after resuming: %+v, next run in the future: %v", runs, future)
	}

	s.call(t, "PATCH", path+"/triggers/"+created.Trigger.ID, a, w.ID, map[string]any{"enabled": false}, nil)
	if _, err := s.Env.Owner.Exec(ctx, `UPDATE autoresearch_trigger SET next_run_at = now() - interval '7 days' WHERE id = $1`, created.Trigger.ID); err != nil {
		t.Fatal(err)
	}
	var enabled struct {
		NextRunAt time.Time `json:"next_run_at"`
	}
	if code := s.call(t, "PATCH", path+"/triggers/"+created.Trigger.ID, a, w.ID, map[string]any{"enabled": true}, &enabled); code != 200 {
		t.Fatalf("enable: %d", code)
	}
	if !enabled.NextRunAt.After(time.Now()) {
		t.Fatalf("enable returned stale next_run_at: %v", enabled.NextRunAt)
	}
	if err := s.Env.Owner.QueryRow(ctx, `SELECT next_run_at > now() FROM autoresearch_trigger WHERE id = $1`, created.Trigger.ID).Scan(&future); err != nil {
		t.Fatal(err)
	}
	if !future {
		t.Fatal("enabling a schedule kept a stale next run")
	}
}

func TestAutoresearchRenderedTemplateBoundary(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	for _, tc := range []struct {
		template string
		code     int
	}{
		{strings.Repeat("界", 190) + "{{date}}", 201},
		{strings.Repeat("界", 191) + "{{date}}", 400},
		{strings.Repeat("x", 180) + "{{date}}{{date}}", 201},
		{strings.Repeat("x", 181) + "{{date}}{{date}}", 400},
	} {
		if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card", "title_template": tc.template}, nil); code != tc.code {
			t.Fatalf("template %q: %d want %d", tc.template, code, tc.code)
		}
	}
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card", "title_template": "original"})
	path := "/api/autoresearch/" + ap.ID
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"title_template": strings.Repeat("x", 181) + "{{date}}{{date}}"}, nil); code != 400 {
		t.Fatalf("update: %d", code)
	}
	var stored string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT title_template FROM autoresearch WHERE id=$1`, ap.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "original" {
		t.Fatalf("rejected update persisted: %q", stored)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"title_template": strings.Repeat("界", 190) + "{{date}}"}, nil); code != 200 {
		t.Fatalf("boundary update: %d", code)
	}
}
func TestAutoresearchTriggerEncryptionAndMembership(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	outsider := s.signIn(t, "outside@lab.org")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card"})
	path := "/api/autoresearch/" + ap.ID
	for _, kind := range []string{"collaborators", "subscribers"} {
		for _, method := range []string{"PUT", "DELETE"} {
			if code := s.call(t, method, path+"/"+kind+"/"+s.userID(t, outsider), a, w.ID, nil, nil); code != 400 {
				t.Fatalf("nonmember %s %s: %d", method, kind, code)
			}
		}
	}
	if code := s.call(t, "GET", path, outsider, w.ID, nil, nil); code != 404 {
		t.Fatalf("nonmember reads: %d", code)
	}
	var made struct {
		Trigger struct{ ID string }
		Token   string
	}
	if code := s.call(t, "POST", path+"/triggers", a, w.ID, map[string]any{"kind": "webhook", "provider": "github", "signing_secret": "secret"}, &made); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var enc []byte
	var hash string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT signing_secret_enc,token_hash FROM autoresearch_trigger WHERE id=$1`, made.Trigger.ID).Scan(&enc, &hash); err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(made.Trigger.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := seal.Open(s.H.Autoresearch.Key, enc, id[:])
	if err != nil || string(plain) != "secret" || hash != auth.Hash(made.Token) {
		t.Fatalf("stored credentials: %q %v", plain, err)
	}
	other := uuid.New()
	if _, err := seal.Open(s.H.Autoresearch.Key, enc, other[:]); err == nil {
		t.Fatal("secret opens against another trigger")
	}
	s.H.Autoresearch.Key = nil
	if code := s.call(t, "POST", path+"/triggers", a, w.ID, map[string]any{"kind": "webhook", "provider": "github", "signing_secret": "secret"}, nil); code != 400 {
		t.Fatalf("missing key: %d", code)
	}
}

func TestAutoresearchRunCompletesWithoutScopedTransactionDeadlock(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap := s.autoresearch(t, a, w.ID, map[string]any{"title": "Digest", "execution_mode": "create_card"})
	path := "/api/autoresearch/" + ap.ID + "/run"
	for _, tc := range []struct {
		scope string
		code  int
	}{{"", 400}, {"invalid", 404}, {w.ID, 201}} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, "POST", s.URL+path, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+a)
		if tc.scope != "" {
			req.Header.Set("X-Workspace-ID", tc.scope)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("run did not return before deadline: %v", err)
		}
		if resp.StatusCode != tc.code {
			resp.Body.Close()
			cancel()
			t.Fatalf("scope %q: %d", tc.scope, resp.StatusCode)
		}
		resp.Body.Close()
		cancel()
	}
	var nullTrigger bool
	var question string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT r.trigger_id IS NULL,c.question FROM autoresearch_run r JOIN card c ON c.id=r.card_id WHERE r.autoresearch_id=$1`, ap.ID).Scan(&nullTrigger, &question); err != nil {
		t.Fatal(err)
	}
	if !nullTrigger || question != "Digest" {
		t.Fatalf("manual defaults: trigger null=%v question=%q", nullTrigger, question)
	}
}
