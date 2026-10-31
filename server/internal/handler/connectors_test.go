package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/internal/science"
)

func TestOwnersControlConnectors(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	list := func(tok string) map[string]connectorView {
		t.Helper()
		var out []connectorView
		if code := s.call(t, "GET", "/api/connectors", tok, w.ID, nil, &out); code != 200 {
			t.Fatalf("list: %d", code)
		}
		m := map[string]connectorView{}
		for _, c := range out {
			m[c.Name] = c
		}
		return m
	}
	m := list(b)
	if len(m) != 72 || !m["ensembl"].Enabled || m["omim"].Enabled || m["omim"].Tier != "restricted" ||
		!strings.Contains(m["omim"].Reason, "confirms the licence") || !m["materials-project"].TakesKey || m["materials-project"].Enabled {
		t.Fatalf("the default states: %d %+v %+v %+v", len(m), m["ensembl"], m["omim"], m["materials-project"])
	}

	admin := s.member(t, a, w.ID, "admin@lab.org", "admin")
	if c := s.call(t, "PUT", "/api/connectors/ensembl", admin, w.ID, map[string]any{"revision": 0, "enabled": false}, nil); c != 403 {
		t.Fatalf("admin catalogue mutation: %d", c)
	}
	const key = "mp-key-1234567"
	if code := s.call(t, "PUT", "/api/connectors/materials-project", b, w.ID, map[string]any{"revision": 0, "key": key}, nil); code != 403 {
		t.Fatalf("a member set a key: %d", code)
	}
	var got json.RawMessage
	if code := s.call(t, "PUT", "/api/connectors/materials-project", a, w.ID, map[string]any{"revision": 0, "key": key}, &got); code != 200 ||
		strings.Contains(string(got), key) || !strings.Contains(string(got), `"key_set":true`) || !strings.Contains(string(got), `"enabled":true`) {
		t.Fatalf("save a key: %d %s", code, got)
	}
	var all json.RawMessage
	s.call(t, "GET", "/api/connectors", a, w.ID, nil, &all)
	if strings.Contains(string(all), key) {
		t.Fatal("the key was read back")
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE type = 'connector.updated' AND payload::text LIKE '%' || $1 || '%'`, key); n != 0 {
		t.Fatal("the key reached the activity log")
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE type = 'connector.updated' AND payload->>'connector' = 'materials-project' AND (payload->>'key_set')::boolean`); n != 1 {
		t.Fatalf("%d activity entries", n)
	}
	if code := s.call(t, "PUT", "/api/connectors/materials-project", a, w.ID, map[string]any{"revision": 1, "key": ""}, nil); code != 200 || list(a)["materials-project"].KeySet {
		t.Fatalf("clear the key: %d", code)
	}

	var omim connectorView
	s.call(t, "PUT", "/api/connectors/omim", a, w.ID, map[string]any{"revision": 0, "licence_confirmed": true}, &omim)
	if !omim.LicenceConfirmed || omim.Enabled || !strings.Contains(omim.Reason, "saves a key for `omim`") {
		t.Fatalf("a confirmed licence still needs omim's key: %+v", omim)
	}
	s.call(t, "PUT", "/api/connectors/omim", a, w.ID, map[string]any{"revision": 1, "key": "omim-key-1234"}, &omim)
	if !omim.Enabled || !omim.LicenceConfirmed {
		t.Fatalf("omim with a licence and a key: %+v", omim)
	}
	var ens connectorView
	s.call(t, "PUT", "/api/connectors/ensembl", a, w.ID, map[string]any{"revision": 0, "enabled": false}, &ens)
	if ens.Enabled || !strings.Contains(ens.Reason, "enables `ensembl`") {
		t.Fatalf("disable: %+v", ens)
	}
	for _, c := range []struct {
		path string
		body map[string]any
		want int
	}{
		{"/api/connectors/nope", map[string]any{"revision": 0, "enabled": true}, 404},
		{"/api/connectors/ensembl", map[string]any{"revision": 0, "key": "abcdefgh123"}, 400},
		{"/api/connectors/omim", map[string]any{"revision": 0, "key": "short"}, 400},
		{"/api/connectors/omim", map[string]any{"revision": 0, "key": "has a space 123"}, 400},
	} {
		if code := s.call(t, "PUT", c.path, a, w.ID, c.body, nil); code != c.want {
			t.Errorf("PUT %s %v: %d, want %d", c.path, c.body, code, c.want)
		}
	}
}

func TestAKeyNeedsTheServersSecret(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.H.Connectors.Secret = nil
	var e struct{ Error string }
	if code := s.call(t, "PUT", "/api/connectors/omim", a, w.ID, map[string]any{"revision": 0, "key": "omim-key-1234"}, &e); code != 400 || !strings.Contains(e.Error, "SECRET_KEY") {
		t.Fatalf("%d %q", code, e.Error)
	}
}

func TestMembersReadTheSkillBundle(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	var skills []science.Skill
	if code := s.call(t, "GET", "/api/skills", b, w.ID, nil, &skills); code != 200 || len(skills) != len(science.Allow) {
		t.Fatalf("%d, %d skills", code, len(skills))
	}
	for _, sk := range skills {
		if sk.Licence == "" || sk.Domain == "" {
			t.Errorf("%+v", sk)
		}
	}
}

func TestConnectorPolicyIsSeparateFromReadiness(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "policy@lab.org")
	ws := s.workspace(t, token, "policy").ID
	var listed []map[string]any
	if code := s.call(t, "GET", "/api/connectors", token, ws, nil, &listed); code != 200 {
		t.Fatal(code)
	}
	for _, row := range listed {
		if row["name"] == "biogrid" {
			if row["policy_enabled"] != true || row["enabled"] != false || row["reason"] == "" {
				t.Fatalf("policy enabled but setup incomplete: %+v", row)
			}
		}
	}
	var saved map[string]any
	if code := s.call(t, "PUT", "/api/connectors/biogrid", token, ws, map[string]any{"revision": 0, "enabled": false}, &saved); code != 200 {
		t.Fatal(code)
	}
	if saved["policy_enabled"] != false || saved["enabled"] != false {
		t.Fatalf("disabled policy: %+v", saved)
	}
	if code := s.call(t, "PUT", "/api/connectors/biogrid", token, ws, map[string]any{"revision": 1, "enabled": true}, &saved); code != 200 {
		t.Fatal(code)
	}
	if saved["policy_enabled"] != true || saved["enabled"] != false || !strings.Contains(saved["reason"].(string), "saves a key") {
		t.Fatalf("policy must not imply readiness: %+v", saved)
	}
}

func TestOwnersSaveTheWorkspacesOpenAlexKey(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	var v connectorView
	if code := s.call(t, "GET", "/api/connectors/openalex", b, w.ID, nil, &v); code != 200 || v.KeySet || !v.TakesKey || !v.Enabled || v.Revision != 0 {
		t.Fatalf("a member reads OpenAlex's state: %d %+v", code, v)
	}
	const key = "oa-key-1234567"
	if code := s.call(t, "PUT", "/api/connectors/openalex", b, w.ID, map[string]any{"revision": 0, "key": key}, nil); code != 403 {
		t.Fatalf("a member set the key: %d", code)
	}
	var got json.RawMessage
	if code := s.call(t, "PUT", "/api/connectors/openalex", a, w.ID, map[string]any{"revision": 0, "key": key}, &got); code != 200 ||
		strings.Contains(string(got), key) || !strings.Contains(string(got), `"key_set":true`) {
		t.Fatalf("save the key: %d %s", code, got)
	}
	s.call(t, "GET", "/api/connectors/openalex", b, w.ID, nil, &got)
	if strings.Contains(string(got), key) || !strings.Contains(string(got), `"key_set":true`) {
		t.Fatalf("read back: %s", got)
	}
	var list []connectorView
	s.call(t, "GET", "/api/connectors", a, w.ID, nil, &list)
	for _, c := range list {
		if c.Name == "openalex" {
			t.Fatal("OpenAlex joined the connector catalogue")
		}
	}
	for _, body := range []map[string]any{{"revision": 1, "enabled": false}, {"revision": 1, "licence_confirmed": true}} {
		if code := s.call(t, "PUT", "/api/connectors/openalex", a, w.ID, body, nil); code != 400 {
			t.Errorf("PUT %v: %d, want 400", body, code)
		}
	}
	if code := s.call(t, "PUT", "/api/connectors/openalex", a, w.ID, map[string]any{"revision": 1, "key": ""}, &v); code != 200 || v.KeySet {
		t.Fatalf("clear the key: %d %+v", code, v)
	}
}
