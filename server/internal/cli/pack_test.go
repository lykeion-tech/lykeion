package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamCommands(t *testing.T) {
	type req struct{ method, path, contentType, body string }
	var got []req
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, req{r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), string(b)})
		switch r.URL.Path {
		case "/api/packs/catalog":
			w.Write([]byte(`[{"name":"demo","version":"1","source":"shipped","start_kinds":["question"]}]`))
		case "/api/packs":
			if r.Method == "POST" {
				w.Write([]byte(`{"id":"p1","name":"mine","version":"2","sha256":"abc"}`))
				return
			}
			w.Write([]byte(`[{"id":"p1","version":"2","source":"uploaded","approvals_by_me":true,"pinned_studies":3}]`))
		case "/api/cards":
			w.Write([]byte(`{"number":1,"title":"q"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "mine.zip")
	os.WriteFile(archive, []byte("PK\x03\x04zip-bytes"), 0o600)

	if out := lyk(t, "", "team", "list"); !strings.Contains(out, "demo") {
		t.Fatalf("list %q", out)
	}
	lyk(t, "", "team", "use", "demo")
	if out := lyk(t, "", "team", "upload", archive); !strings.Contains(out, "p1 mine v2") {
		t.Fatalf("upload %q", out)
	}
	if out := lyk(t, "", "team", "versions", "my team"); !strings.Contains(out, "approved") || !strings.Contains(out, "3 studies") {
		t.Fatalf("versions %q", out)
	}
	lyk(t, "", "team", "approve", "p1")
	lyk(t, "", "card", "new", "q", "--start-kind", "dataset")

	want := []req{
		{"GET", "/api/packs/catalog", "application/json", ""},
		{"PUT", "/api/me/pack", "application/json", `{"name":"demo"}`},
		{"POST", "/api/packs", "application/zip", "PK\x03\x04zip-bytes"},
		{"GET", "/api/packs?name=my+team", "application/json", ""},
		{"POST", "/api/packs/p1/approve", "application/json", ""},
	}
	if len(got) != 6 {
		t.Fatalf("%d requests: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("request %d: %+v, want %+v", i, got[i], w)
		}
	}
	var card map[string]any
	json.Unmarshal([]byte(got[5].body), &card)
	if got[5].path != "/api/cards" || card["start_kind"] != "dataset" {
		t.Fatalf("card new sent %+v", got[5])
	}
}
