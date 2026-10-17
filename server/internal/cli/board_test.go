package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestBoardBlockedActions(t *testing.T) {
	type seen struct {
		Path string
		Body map[string]any
	}
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "bad request", 400)
			return
		}
		if r.Method == "GET" {
			writeResponse(t, w, `[{"id":"c-7","number":7,"board_column":"validation"}]`)
			return
		}
		got = append(got, seen{r.URL.Path, body})
		writeResponse(t, w, `{"id":"c-7","number":7,"board_column":"grounding","state":"working"}`)
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	lyk(t, "", "board", "unblock", "7", "--action", "raise_cap", "--cap", "5")
	lyk(t, "", "board", "close", "#7", "--reason", "enough")
	lyk(t, "", "board", "reopen", "7", "--reason", "retracted", "--evidence", "doi:x")
	lyk(t, "", "board", "revert", "7")
	want := []seen{
		{"/api/cards/c-7/unblock", map[string]any{"action": "raise_cap", "cap": float64(5)}},
		{"/api/cards/c-7/close", map[string]any{"reason": "enough"}},
		{"/api/cards/c-7/reopen", map[string]any{"reason": "retracted", "evidence": "doi:x"}},
		{"/api/cards/c-7/revert-revision", nil},
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("got %s\nwant %s", a, b)
	}
}
