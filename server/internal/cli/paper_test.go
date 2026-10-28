package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPaperCLI(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/run/coresearch/paper/main.tex" {
				t.Error(r.URL.Path)
			}
			fmt.Fprint(w, `{"version":7,"text":"paper"}`)
		})
		var out, errout strings.Builder
		cmd := Root(strings.NewReader(""), &out)
		cmd.SetErr(&errout)
		cmd.SetArgs([]string{"paper", "get", "main.tex"})
		e := cmd.Execute()
		if e != nil || out.String() != "paper" || !strings.Contains(errout.String(), "X-Version: 7") {
			t.Fatalf("%s %s %v", out.String(), errout.String(), e)
		}
	})
	t.Run("edit", func(t *testing.T) {
		coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Path  string
				Base  int
				Hunks []struct{ Find, Replace string }
			}
			json.NewDecoder(r.Body).Decode(&in)
			if r.URL.Path != "/api/run/coresearch/paper/edit" || in.Path != "main.tex" || in.Base != 4 || len(in.Hunks) != 1 || in.Hunks[0].Find != "old" {
				t.Errorf("request %+v", in)
			}
			fmt.Fprint(w, `{"version":8,"results":[{"index":0,"applied":false,"current":"current text"}]}`)
		})
		out, e := coresearchExecute(`[{"find":"old","replace":"new"}]`, "paper", "edit", "main.tex", "--base", "4")
		if ExitCode(e) != 3 || !strings.Contains(out, "current text") {
			t.Fatalf("%s %v", out, e)
		}
		_, e = coresearchExecute(`bad`, "paper", "edit", "main.tex", "--base", "4")
		if ExitCode(e) != 1 {
			t.Fatal(e)
		}
	})
	t.Run("build", func(t *testing.T) {
		coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				fmt.Fprint(w, `{"build_id":"b1"}`)
			} else {
				if r.URL.Path != "/api/run/coresearch/paper/build/b1" {
					t.Error(r.URL.Path)
				}
				fmt.Fprint(w, `{"id":"b1","status":"ok"}`)
			}
		})
		_, e := coresearchExecute("", "paper", "build")
		if e != nil {
			t.Fatal(e)
		}
	})
	t.Run("no machine", func(t *testing.T) {
		old := paperBuildWait
		paperBuildWait = 3 * time.Millisecond
		t.Cleanup(func() { paperBuildWait = old })
		coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				fmt.Fprint(w, `{"build_id":"b1"}`)
			} else {
				fmt.Fprint(w, `{"id":"b1","status":"queued"}`)
			}
		})
		_, e := coresearchExecute("", "paper", "build")
		if ExitCode(e) != 2 {
			t.Fatal(e)
		}
	})
}

func TestPaperCLINestedPath(t *testing.T) {
	coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/run/coresearch/paper/sections/intro.tex" {
			t.Errorf("nested file path encoded as a single route segment: %s", r.URL.EscapedPath())
		}
		fmt.Fprint(w, `{"version":0,"text":"nested"}`)
	})
	out, e := coresearchExecute("", "paper", "get", "sections/intro.tex")
	if e != nil || out != "nested" {
		t.Fatalf("%q %v", out, e)
	}
}
