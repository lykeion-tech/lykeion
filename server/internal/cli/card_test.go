package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCardNewSendsEnv(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/cards" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","number":1,"title":"q"}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	e := &env{cfg: Config{Server: srv.URL, Token: "ltk_run", Workspace: "w"}, out: &out}
	cmd := cardCmd(e)
	cmd.SetArgs([]string{"new", "q", "--env", "A=1", "--env", "B=2", "--env", "C=a,b=c"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"A": "1", "B": "2", "C": "a,b=c"}
	if !reflect.DeepEqual(posted["env"], want) {
		t.Fatalf("posted env %#v, want %#v", posted["env"], want)
	}
	posted = nil
	cmd = cardCmd(e)
	cmd.SetArgs([]string{"new", "q", "--env", "A"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "NAME=VALUE") {
		t.Fatalf("missing value: %v", err)
	}
	if posted != nil {
		t.Fatal("invalid env reached server")
	}
}
