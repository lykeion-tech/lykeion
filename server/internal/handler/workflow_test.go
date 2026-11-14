package handler

import (
	"net/http"
	"net/url"
	"testing"
)

func TestWorkflowLibraryNeedsSignIn(t *testing.T) {
	s := newServer(t)
	for _, p := range []string{"/api/workflows", "/api/workflows/mendota-ice-phenology", "/api/workflows/mendota-ice-phenology/files?path=tests/test.sh"} {
		if c := s.call(t, "GET", p, "", "", nil, nil); c != 401 {
			t.Fatalf("%s without a session: %d", p, c)
		}
	}
}

func TestWorkflowLibraryListsAndServes(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "ada@lab.org")
	var list struct {
		Release   string
		Workflows []map[string]any
	}
	if c := s.call(t, "GET", "/api/workflows", tok, "", nil, &list); c != 200 || list.Release == "" || len(list.Workflows) != 70 {
		t.Fatalf("list: %d %q %d", c, list.Release, len(list.Workflows))
	}
	if _, has := list.Workflows[0]["files"]; has {
		t.Fatal("list entries carry files")
	}
	var d struct {
		Instruction string
		Readme      string
		Files       []struct{ Path string }
	}
	if c := s.call(t, "GET", "/api/workflows/mendota-ice-phenology", tok, "", nil, &d); c != 200 || d.Instruction == "" {
		t.Fatalf("detail: %d", c)
	}
	found := false
	for _, f := range d.Files {
		found = found || f.Path == "tests/test.sh"
	}
	if !found {
		t.Fatal("detail lacks tests/test.sh")
	}
	req, _ := http.NewRequest("GET", s.URL+"/api/workflows/mendota-ice-phenology/files?path=tests/test.sh", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") == "" {
		t.Fatalf("file: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestWorkflowFilesRefuseEscapes(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "ada@lab.org")
	var d struct {
		Files []struct {
			Path     string
			Embedded bool
		}
	}
	s.call(t, "GET", "/api/workflows/mendota-ice-phenology", tok, "", nil, &d)
	data := ""
	for _, f := range d.Files {
		if !f.Embedded {
			data = f.Path
			break
		}
	}
	if data == "" {
		t.Fatal("no non-embedded file to probe")
	}
	for _, c := range []struct{ slug, path string }{
		{"mendota-ice-phenology", "../index.json"},
		{"mendota-ice-phenology", "/etc/passwd"},
		{"mendota-ice-phenology", data},
		{"no-such-workflow", "tests/test.sh"},
	} {
		p := "/api/workflows/" + c.slug + "/files?path=" + url.QueryEscape(c.path)
		if code := s.call(t, "GET", p, tok, "", nil, nil); code != 404 {
			t.Fatalf("%s: %d", p, code)
		}
	}
}
