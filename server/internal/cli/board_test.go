package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoardAndShowReadTheStudysState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cards":
			w.Write([]byte(`[{"id":"a","number":1,"title":"Scored low","board_column":"hypotheses","state":"blocked","flag":""},
				{"id":"b","number":2,"title":"At work","board_column":"grounding","state":"working","flag":""}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	out := lyk(t, "", "board")
	blocked, active, _ := strings.Cut(strings.TrimPrefix(out, "== Blocked\n"), "== Active\n")
	if !strings.Contains(blocked, "#1") || strings.Contains(blocked, "#2") || !strings.Contains(active, "#2") || strings.Contains(active, "#1") {
		t.Fatalf("board:\n%q", out)
	}
	if show := lyk(t, "", "card", "show", "1"); strings.Contains(show, "flag:") || strings.Contains(show, "spent") || !strings.Contains(show, "state: blocked") {
		t.Fatalf("show:\n%s", show)
	}
}
