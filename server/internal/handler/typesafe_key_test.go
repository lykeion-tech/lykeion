package handler

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/jev"
)

func TestTypesafeKeyIsSealedAndNeverReturned(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "key@lab.org")
	other := s.signIn(t, "other@lab.org")
	var me map[string]any
	if s.call(t, "GET", "/api/me", tok, "", nil, &me); me["typesafe_key_set"] != false {
		t.Fatalf("before: %v", me["typesafe_key_set"])
	}
	if c := s.call(t, "PUT", "/api/me/typesafe-key", tok, "", map[string]string{"key": "  "}, nil); c != 400 {
		t.Fatalf("blank key: %d", c)
	}
	if c := s.call(t, "PUT", "/api/me/typesafe-key", tok, "", map[string]string{"key": " ts-secret-key "}, nil); c != 204 {
		t.Fatalf("save: %d", c)
	}
	var raw map[string]any
	s.call(t, "GET", "/api/me", tok, "", nil, &raw)
	if raw["typesafe_key_set"] != true || strings.Contains(strings.ToLower(strings.Join(keysOf(raw), " ")), "cipher") {
		t.Fatalf("after save: %v", raw)
	}
	if s.call(t, "GET", "/api/me", other, "", nil, &me); me["typesafe_key_set"] != false {
		t.Fatal("another person shows a key")
	}
	var id uuid.UUID
	var sealed []byte
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT k.user_id, k.ciphertext FROM user_typesafe_key k JOIN "user" u ON u.id = k.user_id WHERE u.email = 'key@lab.org'`).Scan(&id, &sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "ts-secret-key") {
		t.Fatal("key stored in the clear")
	}
	if k, err := jev.OpenKey(s.H.Connectors.Secret, id, sealed); err != nil || k != "ts-secret-key" {
		t.Fatalf("open: %q %v", k, err)
	}
	if c := s.call(t, "DELETE", "/api/me/typesafe-key", tok, "", nil, nil); c != 204 {
		t.Fatalf("delete: %d", c)
	}
	if s.call(t, "GET", "/api/me", tok, "", nil, &me); me["typesafe_key_set"] != false {
		t.Fatal("key still set after delete")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
