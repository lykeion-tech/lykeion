package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func (s *server) addonEvents(t *testing.T, ws string) int {
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'stage_addon.updated'`, ws).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAddonsAreOwnerOnlyAndEmitAnEvent(t *testing.T) {
	s := newServer(t)
	a, b, c := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org"), s.signIn(t, "c@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "admin")+"/accept", b, "", nil, nil)
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "c@lab.org", "member")+"/accept", c, "", nil, nil)
	path := "/api/workspaces/" + w.ID + "/addons/grounding"
	body := map[string]any{"skill_md": "  Also search the lab index.\n", "mcp": []map[string]string{{"name": "lab-index", "url": "https://tools.lab.org/mcp"}}}
	for _, tok := range []string{b, c} {
		if code := s.call(t, "PATCH", path, tok, "", body, nil); code != 403 {
			t.Fatalf("a non-owner changed an add-on: %d", code)
		}
	}
	if s.addonEvents(t, w.ID) != 0 {
		t.Fatal("a refused change emitted an event")
	}

	type view struct {
		BoardColumn string `json:"board_column"`
		SkillMD     string `json:"skill_md"`
		MCP         []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"mcp"`
		AddonHash string `json:"addon_hash"`
	}
	var got view
	if code := s.call(t, "PATCH", path, a, "", body, &got); code != 200 || got.SkillMD != "Also search the lab index." ||
		len(got.MCP) != 1 || got.MCP[0].Name != "lab-index" || len(got.AddonHash) != 64 {
		t.Fatalf("owner: %d %+v", code, got)
	}
	if s.addonEvents(t, w.ID) != 1 {
		t.Fatal("no stage_addon.updated event")
	}
	var payload string
	s.Env.Owner.QueryRow(t.Context(), `SELECT payload->>'addon_hash' FROM event WHERE workspace_id = $1 AND type = 'stage_addon.updated'`, w.ID).Scan(&payload)
	if payload != got.AddonHash {
		t.Fatalf("event hash %q, row hash %q", payload, got.AddonHash)
	}

	first := got.AddonHash
	if code := s.call(t, "PATCH", path, a, "", map[string]any{"skill_md": "Prefer mouse studies."}, &got); code != 200 ||
		got.SkillMD != "Prefer mouse studies." || len(got.MCP) != 1 || got.AddonHash == first {
		t.Fatalf("a field left out was not kept: %d %+v", code, got)
	}
	if code := s.call(t, "PATCH", path, a, "", map[string]any{"mcp": []any{}}, &got); code != 200 ||
		got.SkillMD != "Prefer mouse studies." || len(got.MCP) != 0 {
		t.Fatalf("clearing servers: %d %+v", code, got)
	}

	var list []view
	if code := s.call(t, "GET", "/api/workspaces/"+w.ID+"/addons", c, "", nil, &list); code != 200 || len(list) != 1 || list[0].BoardColumn != "grounding" {
		t.Fatalf("a member reading add-ons: %d %+v", code, list)
	}
}

func TestAddonRulesAtTheRoute(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	base := "/api/workspaces/" + w.ID + "/addons/"
	one := func(name, url string) []map[string]string { return []map[string]string{{"name": name, "url": url}} }
	var nine []map[string]string
	for i := 0; i < 9; i++ {
		nine = append(nine, map[string]string{"name": fmt.Sprintf("s%d", i), "url": "https://t.org/mcp"})
	}
	for _, c := range []struct {
		name, column string
		body         any
		want         string
	}{
		{"backlog column", "backlog", map[string]any{"skill_md": "x"}, "column must be"},
		{"unknown column", "nope", map[string]any{"skill_md": "x"}, "column must be"},
		{"sources name", "grounding", map[string]any{"mcp": one("sources", "https://t.org/mcp")}, "reserved"},
		{"lyk name", "grounding", map[string]any{"mcp": one("lyk_sources", "https://t.org/mcp")}, "reserved"},
		{"stdio", "grounding", map[string]any{"mcp": []map[string]any{{"name": "x", "command": "npx", "args": []string{"server"}}}}, "stdio"},
		{"headers", "grounding", map[string]any{"mcp": []map[string]any{{"name": "x", "url": "https://t.org/mcp", "headers": map[string]string{"Authorization": "Bearer s"}}}}, "credentials"},
		{"user info", "grounding", map[string]any{"mcp": one("x", "https://u:p@t.org/mcp")}, "credentials"},
		{"token in url", "grounding", map[string]any{"mcp": one("x", "https://t.org/mcp?token=s")}, "query"},
		{"too many", "grounding", map[string]any{"mcp": nine}, "at most 8"},
		{"too long", "grounding", map[string]any{"skill_md": strings.Repeat("a", 16<<10+1)}, "16384"},
		{"nul", "grounding", map[string]any{"skill_md": "a\u0000b"}, "NUL"},
		{"gate field", "grounding", map[string]any{"skill_md": "x", "gate": map[string]any{"checks": []string{}}}, "only skill_md and mcp"},
	} {
		var e struct{ Error string }
		if code := s.call(t, "PATCH", base+c.column, a, "", c.body, &e); code != 400 || !strings.Contains(e.Error, c.want) {
			t.Errorf("%s: %d %q", c.name, code, e.Error)
		}
	}
	var rows int
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM stage_addon WHERE workspace_id = $1`, w.ID).Scan(&rows)
	if rows != 0 || s.addonEvents(t, w.ID) != 0 {
		t.Fatalf("refused changes left %d rows and %d events", rows, s.addonEvents(t, w.ID))
	}
}

func TestAddonRejectsRawInvalidUTF8AndNullWithoutMutation(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	path := "/api/workspaces/" + w.ID + "/addons/grounding"
	var before struct {
		SkillMD   string `json:"skill_md"`
		AddonHash string `json:"addon_hash"`
	}
	if code := s.call(t, "PATCH", path, a, "", map[string]any{"skill_md": "keep", "mcp": []map[string]string{{"name": "lab", "url": "https://lab.org/mcp"}}}, &before); code != 200 {
		t.Fatalf("seed: %d", code)
	}
	for _, raw := range []string{`{"mcp":null}`, "{\"skill_md\":\"bad" + string([]byte{0xff}) + "\"}"} {
		req, err := http.NewRequest("PATCH", s.URL+path, strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+a)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("raw request %q: %d", raw, resp.StatusCode)
		}
	}
	var hash, text string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT skill_md FROM stage_addon WHERE workspace_id=$1`, w.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	var list []struct {
		AddonHash string `json:"addon_hash"`
	}
	s.call(t, "GET", "/api/workspaces/"+w.ID+"/addons", a, "", nil, &list)
	if len(list) == 1 {
		hash = list[0].AddonHash
	}
	if text != before.SkillMD || hash != before.AddonHash || s.addonEvents(t, w.ID) != 1 {
		t.Fatalf("invalid requests mutated row/events: text=%q hash=%q events=%d", text, hash, s.addonEvents(t, w.ID))
	}
}

func TestAddonJSONRetainsBoundsAndStrictFields(t *testing.T) {
	for _, raw := range []string{`{"gate":{}}`, `{} {}`, `{"skill_md":"` + strings.Repeat("a", 1<<20) + `"}`} {
		r, err := http.NewRequest("PATCH", "/api/workspaces/id/addons/grounding", strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		var in struct {
			SkillMD *string `json:"skill_md"`
			MCP     any     `json:"mcp"`
		}
		if err := readAddonJSON(r, &in); err == nil {
			t.Fatal("invalid body accepted")
		}
	}
}
