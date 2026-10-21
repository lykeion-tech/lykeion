package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
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
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	if out := lyk(t, "", "team", "add", "p1"); !strings.Contains(out, "added p1") {
		t.Fatalf("add %q", out)
	}
	lyk(t, "", "team", "approve", "p1") // the older name still adds

	if out := lyk(t, "", "team", "--help"); strings.Contains(out, "upload") || strings.Contains(out, "version") {
		t.Fatalf("removed command in help: %s", out)
	}
	want := []req{
		{"POST", "/api/teams/p1/add", "application/json", ""},
		{"POST", "/api/teams/p1/add", "application/json", ""},
	}
	if len(got) != 2 {
		t.Fatalf("%d requests: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("request %d: %+v, want %+v", i, got[i], w)
		}
	}
}
