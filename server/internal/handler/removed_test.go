package handler

import (
	"bytes"
	"strings"
	"testing"

	"lykeion/server/internal/cli"
)

func TestGateEraRoutesAreGone(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "q")
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/cards/" + c.ID + "/send-back"},
		{"POST", "/api/cards/" + c.ID + "/budget"},
		{"POST", "/api/cards/" + c.ID + "/unblock"},
		{"POST", "/api/cards/" + c.ID + "/close"},
		{"POST", "/api/cards/" + c.ID + "/revert-revision"},
		{"POST", "/api/cards/" + c.ID + "/reopen"},
		{"POST", "/api/cards/" + c.ID + "/pack"},
		{"PUT", "/api/me/pack"},
		{"GET", "/api/cards/" + c.ID + "/gates"},
		{"GET", "/api/packs/catalog"},
	} {
		if code := s.call(t, r.method, r.path, a, w.ID, map[string]string{}, nil); code != 404 && code != 405 {
			t.Errorf("%s %s answers %d", r.method, r.path, code)
		}
	}

	var help bytes.Buffer
	root := cli.Root(strings.NewReader(""), &help)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "\n  card ") {
		t.Fatalf("the help does not list the commands:\n%s", help.String())
	}
	for _, line := range strings.Split(help.String(), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			switch f[0] {
			case "ground", "hyp", "review", "install":
				t.Errorf("lyk --help still lists %q", f[0])
			}
		}
	}
}
