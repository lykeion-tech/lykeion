package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDisplayPrefix(t *testing.T) {
	for _, c := range []struct{ prefix, slug, want string }{
		{"BETA", "smith-lab", "BETA"},
		{"", "smith-lab", "SMIT"},
		{"", "ab-12", "AB"},
		{"", "x1", "LYK"},
		{"", "42-lab", "LAB"},
	} {
		if got := displayPrefix(c.prefix, c.slug); got != c.want {
			t.Errorf("displayPrefix(%q, %q) = %q, want %q", c.prefix, c.slug, got, c.want)
		}
	}
}

type wsView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	ResearchContext string `json:"research_context"`
	CardPrefix      string `json:"card_prefix"`
	LogoBlob        string `json:"logo_blob"`
	DisplayPrefix   string `json:"display_prefix"`
}

func TestWorkspaceGeneralFields(t *testing.T) {
	s := newServer(t)
	a, b, c := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org"), s.signIn(t, "c@lab.org")
	w := s.workspace(t, a, "smith-lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "admin")+"/accept", b, "", nil, nil)
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "c@lab.org", "member")+"/accept", c, "", nil, nil)
	path := "/api/workspaces/" + w.ID

	var got wsView
	if code := s.call(t, "GET", path, c, "", nil, &got); code != 200 || got.DisplayPrefix != "SMIT" || got.CardPrefix != "" {
		t.Fatalf("derived prefix: %d %+v", code, got)
	}
	body := map[string]any{"description": "Liver metabolism", "research_context": "We study fasting in mice.", "card_prefix": "BETA"}
	if code := s.call(t, "PATCH", path, c, "", body, nil); code != 403 {
		t.Fatalf("a member edited the workspace: %d", code)
	}
	if code := s.call(t, "PATCH", path, b, "", body, &got); code != 200 {
		t.Fatalf("admin edit: %d", code)
	}
	if got.Name != "Lab smith-lab" || got.Description != "Liver metabolism" || got.ResearchContext != "We study fasting in mice." ||
		got.CardPrefix != "BETA" || got.DisplayPrefix != "BETA" {
		t.Fatalf("edited: %+v", got)
	}
	var list []wsView
	s.call(t, "GET", "/api/workspaces", c, "", nil, &list)
	if len(list) != 1 || list[0].DisplayPrefix != "BETA" || list[0].ResearchContext != "We study fasting in mice." || list[0].Description != "Liver metabolism" {
		t.Fatalf("list: %+v", list)
	}
	for _, bad := range []map[string]any{{}, {"name": " "}, {"card_prefix": "beta"}, {"card_prefix": "B"}, {"card_prefix": "ABCDEFG"}, {"slug": "other"}} {
		if code := s.call(t, "PATCH", path, a, "", bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
	if code := s.call(t, "PATCH", path, a, "", map[string]any{"card_prefix": ""}, &got); code != 200 || got.DisplayPrefix != "SMIT" || got.Description != "Liver metabolism" {
		t.Fatalf("prefix cleared: %d %+v", code, got)
	}
	var updated int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'workspace.updated'`, w.ID).Scan(&updated); err != nil || updated != 2 {
		t.Fatalf("workspace.updated events: %d %v", updated, err)
	}
}

func (s *server) logoRequest(t *testing.T, method, wsID, token, typ string, body []byte) (int, string, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, s.URL+"/api/workspaces/"+wsID+"/logo", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if typ != "" {
		req.Header.Set("Content-Type", typ)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), out
}

func TestWorkspaceLogo(t *testing.T) {
	s := newServer(t)
	a, c, x := s.signIn(t, "a@lab.org"), s.signIn(t, "c@lab.org"), s.signIn(t, "x@else.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "c@lab.org", "member")+"/accept", c, "", nil, nil)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 200)...)

	if code, _, _ := s.logoRequest(t, "GET", w.ID, c, "", nil); code != 404 {
		t.Fatalf("no logo yet: %d", code)
	}
	for _, tc := range []struct {
		token, typ string
		body       []byte
		want       int
	}{
		{c, "image/png", png, 403},
		{x, "image/png", png, 404},
		{a, "text/plain", png, 415},
		{a, "image/jpeg", png, 400},
		{a, "image/png", nil, 400},
		{a, "image/png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxLogoBytes)...), 413},
	} {
		if code, _, _ := s.logoRequest(t, "PUT", w.ID, tc.token, tc.typ, tc.body); code != tc.want {
			t.Fatalf("PUT %s %d bytes: %d, want %d", tc.typ, len(tc.body), code, tc.want)
		}
	}
	if code, _, _ := s.logoRequest(t, "PUT", w.ID, a, "image/png", png); code != 204 {
		t.Fatalf("upload: %d", code)
	}
	code, typ, got := s.logoRequest(t, "GET", w.ID, c, "", nil)
	if code != 200 || typ != "image/png" || !bytes.Equal(got, png) {
		t.Fatalf("read back: %d %q %d bytes", code, typ, len(got))
	}
	if code, _, _ := s.logoRequest(t, "GET", w.ID, x, "", nil); code != 404 {
		t.Fatalf("a stranger read the logo: %d", code)
	}
	var view wsView
	s.call(t, "GET", "/api/workspaces/"+w.ID, a, "", nil, &view)
	if view.LogoBlob == "" {
		t.Fatal("logo_blob not set")
	}
	var blobs int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'blob.created' AND payload->>'blob_key' = $2`, w.ID, view.LogoBlob).Scan(&blobs); err != nil || blobs != 1 {
		t.Fatalf("blob.created: %d %v", blobs, err)
	}
}

func TestProfileFields(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "p@lab.org")
	var me struct {
		Name        string  `json:"name"`
		Timezone    string  `json:"timezone"`
		CareerStage string  `json:"career_stage"`
		OnboardedAt *string `json:"onboarded_at"`
	}
	if s.call(t, "GET", "/api/me", tok, "", nil, &me); me.CareerStage != "" || me.OnboardedAt != nil {
		t.Fatalf("defaults: %+v", me)
	}
	if code := s.call(t, "PATCH", "/api/me", tok, "", map[string]any{"name": " Ada ", "career_stage": "phd"}, &me); code != 200 ||
		me.Name != "Ada" || me.CareerStage != "phd" || me.Timezone != "UTC" || me.OnboardedAt != nil {
		t.Fatalf("profile: %d %+v", code, me)
	}
	for _, bad := range []map[string]any{{}, {"name": " "}, {"career_stage": "wizard"}, {"onboarded": false}, {"timezone": "Local"}, {"email": "x@y"}} {
		if code := s.call(t, "PATCH", "/api/me", tok, "", bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
	if code := s.call(t, "PATCH", "/api/me", tok, "", map[string]any{"onboarded": true}, &me); code != 200 || me.OnboardedAt == nil || me.Name != "Ada" {
		t.Fatalf("onboarded: %d %+v", code, me)
	}
	first := *me.OnboardedAt
	s.call(t, "PATCH", "/api/me", tok, "", map[string]any{"onboarded": true, "career_stage": ""}, &me)
	if me.OnboardedAt == nil || *me.OnboardedAt != first || me.CareerStage != "" {
		t.Fatalf("second onboarding moved the stamp or kept the stage: %+v", me)
	}
	if s.call(t, "GET", "/api/me", tok, "", nil, &me); me.OnboardedAt == nil || *me.OnboardedAt != first {
		t.Fatalf("not stored: %+v", me)
	}
}

func TestWorkspaceReadsCarryTheEventCursor(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	other := s.workspace(t, a, "other")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "q"}, &c)
	latest := func(ws string) int64 {
		var id int64
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT COALESCE(max(id), 0) FROM event WHERE workspace_id = $1`, ws).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM event WHERE workspace_id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	var one struct {
		EventCursor int64           `json:"event_cursor"`
		Settings    json.RawMessage `json:"settings"`
	}
	if code := s.call(t, "GET", "/api/workspaces/"+w.ID, a, "", nil, &one); code != 200 || one.EventCursor == 0 || one.EventCursor != latest(w.ID) {
		t.Fatalf("one: %d %+v, want cursor %d", code, one, latest(w.ID))
	}
	if string(one.Settings) != "{}" {
		t.Fatalf("settings: %s", one.Settings)
	}
	var list []struct {
		ID          string `json:"id"`
		EventCursor int64  `json:"event_cursor"`
	}
	s.call(t, "GET", "/api/workspaces", a, "", nil, &list)
	got := map[string]int64{}
	for _, row := range list {
		got[row.ID] = row.EventCursor
	}
	if len(got) != 2 || got[w.ID] != latest(w.ID) || got[other.ID] != 0 {
		t.Fatalf("list cursors %v, want %s=%d and %s=0", got, w.ID, latest(w.ID), other.ID)
	}
}

func TestInvitationMailLinksTheDashboard(t *testing.T) {
	s := newServer(t)
	s.H.PublicURL = "https://lab.example.org"
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	token := s.invite(t, a, w.ID, "new@lab.org", "member")
	body := s.Mail.last["new@lab.org"]
	for _, want := range []string{"https://lab.example.org/#/invite/" + token, "lyk workspace accept " + token, "POST /api/invitations/" + token + "/accept"} {
		if !strings.Contains(body, want) {
			t.Fatalf("invitation mail lacks %q:\n%s", want, body)
		}
	}
}

func TestProfileFieldsEachWorkAlone(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "solo@lab.org")
	var me struct {
		Name        string  `json:"name"`
		Timezone    string  `json:"timezone"`
		CareerStage string  `json:"career_stage"`
		OnboardedAt *string `json:"onboarded_at"`
	}
	for _, step := range []struct {
		body  map[string]any
		check func() bool
	}{
		{map[string]any{"name": "Ada"}, func() bool { return me.Name == "Ada" }},
		{map[string]any{"timezone": "Europe/Copenhagen"}, func() bool { return me.Timezone == "Europe/Copenhagen" && me.Name == "Ada" }},
		{map[string]any{"career_stage": "postdoc"}, func() bool { return me.CareerStage == "postdoc" && me.Timezone == "Europe/Copenhagen" }},
		{map[string]any{"onboarded": true}, func() bool { return me.OnboardedAt != nil && me.CareerStage == "postdoc" }},
	} {
		if code := s.call(t, "PATCH", "/api/me", tok, "", step.body, &me); code != 200 || !step.check() {
			t.Fatalf("%v: %d %+v", step.body, code, me)
		}
	}
}
