package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"lykeion/server/internal/apiclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	cmd.SetArgs([]string{"new", "q", "--env", "A=first", "--env", "A=second"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"A": "second"}; !reflect.DeepEqual(posted["env"], want) {
		t.Fatalf("repeated name posted env %#v, want %#v", posted["env"], want)
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
func TestCardNewUploadsAServiceFolder(t *testing.T) {
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "server.py"), []byte("print(1)"), 0600)
	uploads := 0
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/attachments" {
			uploads++
			mr, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			kind := ""
			found := false
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					return
				}
				if p.FormName() == "kind" {
					b, _ := io.ReadAll(p)
					kind = string(b)
				}
				if p.FormName() == "file" {
					tr := tar.NewReader(p)
					h, err := tr.Next()
					if err != nil || h.Name != "server.py" {
						t.Errorf("tar %v %v", h, err)
						return
					}
					b, _ := io.ReadAll(tr)
					if string(b) != "print(1)" {
						t.Errorf("content %q", b)
					}
					found = true
				}
			}
			if kind != "service" || !found {
				t.Errorf("kind %q file %v", kind, found)
			}
			w.Write([]byte(`{"id":"uploaded"}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&posted)
		w.Write([]byte(`{"id":"x","number":1,"title":"q"}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	e := &env{cfg: Config{Server: srv.URL, Token: "lyk_person", Workspace: "w"}, out: &out}
	cmd := cardCmd(e)
	cmd.SetArgs([]string{"new", "q", "--service", "evaluator=" + folder, "--service-start", "evaluator=python server.py", "--service-env", "evaluator=DATA", "--env", "DATA=x"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	services, ok := posted["services"].([]any)
	if !ok || len(services) != 1 || services[0].(map[string]any)["attachment_id"] != "uploaded" {
		t.Fatalf("posted %v", posted)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("harmless outside sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(folder, "escape")); err != nil {
		t.Fatal(err)
	}
	cmd = cardCmd(e)
	cmd.SetArgs([]string{"new", "q", "--service", "evaluator=" + folder, "--service-start", "evaluator=python server.py"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("outside link accepted")
	}
	if uploads != 1 {
		t.Fatalf("unsafe folder uploaded %d", uploads)
	}
}

func TestCardNewUploadsAnAttachment(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(file, []byte("public data"), 0600); err != nil {
		t.Fatal(err)
	}
	var posted map[string]any
	uploads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/attachments":
			uploads++
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if string(b) != "public data" {
				t.Errorf("upload %q", b)
			}
			w.Write([]byte(`{"id":"file-upload"}`))
		case "/api/cards":
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Error(err)
			}
			w.Write([]byte(`{"number":1,"title":"q"}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	e := &env{cfg: Config{Server: srv.URL, Token: "lyk_person", Workspace: "w"}, out: &out}
	cmd := cardCmd(e)
	cmd.SetArgs([]string{"new", "q", "--attach", file})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(posted["attachment_ids"], []any{"file-upload"}) || uploads != 1 {
		t.Fatalf("posted %v uploads %d", posted, uploads)
	}
}

func TestCardNewChecksCombinedUploadCountBeforeUploading(t *testing.T) {
	for _, ordinary := range []int{97, 96} {
		t.Run(fmt.Sprintf("%d-files-4-services", ordinary), func(t *testing.T) {
			folder := t.TempDir()
			file := filepath.Join(t.TempDir(), "file.txt")
			if err := os.WriteFile(file, []byte("public"), 0600); err != nil {
				t.Fatal(err)
			}
			uploads, cards := 0, 0
			var posted struct {
				AttachmentIDs []string         `json:"attachment_ids"`
				Services      []map[string]any `json:"services"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/attachments":
					uploads++
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Error(err)
					}
					fmt.Fprintf(w, `{"id":"upload-%d"}`, uploads)
				case "/api/cards":
					cards++
					if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
						t.Error(err)
					}
					w.Write([]byte(`{"number":1,"title":"q"}`))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			var out bytes.Buffer
			e := &env{cfg: Config{Server: srv.URL, Token: "lyk_person", Workspace: "w"}, out: &out}
			args := []string{"new", "q"}
			for i := 0; i < ordinary; i++ {
				args = append(args, "--attach", file)
			}
			for i := 0; i < 4; i++ {
				name := fmt.Sprintf("service-%d", i)
				args = append(args, "--service", name+"="+folder, "--service-start", name+"=python server.py")
			}
			cmd := cardCmd(e)
			cmd.SetArgs(args)
			err := cmd.Execute()
			if ordinary == 97 {
				if err == nil || !strings.Contains(err.Error(), "at most 100 files") {
					t.Fatalf("combined over-limit error %v (uploads=%d cards=%d)", err, uploads, cards)
				}
				if uploads != 0 || cards != 0 {
					t.Fatalf("refusal sent %d uploads and %d cards", uploads, cards)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if uploads != 100 || cards != 1 || len(posted.AttachmentIDs) != 96 || len(posted.Services) != 4 {
				t.Fatalf("boundary uploads=%d cards=%d files=%d services=%d", uploads, cards, len(posted.AttachmentIDs), len(posted.Services))
			}
		})
	}
}

func TestCardServiceClientCollisionsFailBeforeUpload(t *testing.T) {
	for _, pair := range [][2]string{{"one/client", "two/client"}, {"one/CLIENT", "two/client"}, {"one/café", "two/cafe\u0301"}} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"id":"uploaded"}`)) }))
		flags := cardUploadFlags{services: []string{"first=" + t.TempDir(), "second=" + t.TempDir()}, starts: []string{"first=run", "second=run"}, clients: []string{"first=" + pair[0], "second=" + pair[1]}}
		err := flags.upload(context.Background(), &apiclient.Client{Server: srv.URL}, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "duplicate hidden service client filename:") || calls != 0 {
			t.Fatalf("%v: calls=%d err=%v", pair, calls, err)
		}
		flags.clients = []string{"first=" + pair[0], "second=two/different"}
		if err := flags.upload(context.Background(), &apiclient.Client{Server: srv.URL}, map[string]any{}); err != nil || calls != 2 {
			t.Fatalf("distinct: calls=%d err=%v", calls, err)
		}
		srv.Close()
	}
}

func TestCardNewAndCardMachineFlags(t *testing.T) {
	var posted map[string]any
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			w.Write([]byte(`[{"id":"card-id","number":1}]`))
			return
		}
		method, path = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"id":"card-id","number":1,"title":"q"}`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	e := &env{cfg: Config{Server: srv.URL, Token: "ltk_run", Workspace: "w"}, out: &out}
	for _, tc := range []struct {
		args         []string
		method, path string
		machine      any
	}{
		{[]string{"new", "q", "--machine", "lab"}, "POST", "/api/cards", "lab"},
		{[]string{"machine", "1", "daemon-id"}, "PATCH", "/api/cards/card-id/machine", "daemon-id"},
		{[]string{"machine", "1", "mine"}, "PATCH", "/api/cards/card-id/machine", nil},
	} {
		cmd := cardCmd(e)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if method != tc.method || path != tc.path || posted["machine"] != tc.machine {
			t.Fatalf("%s %s %+v", method, path, posted)
		}
	}
}

func TestCardNewSendsPredicates(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	cmd.SetArgs([]string{"new", "q", "--predicate", "effect=effect > 0.2", "--predicate", "gap=a = b"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"name": "effect", "text": "effect > 0.2"}, map[string]any{"name": "gap", "text": "a = b"}}
	if !reflect.DeepEqual(posted["predicates"], want) {
		t.Fatalf("posted predicates %#v, want %#v", posted["predicates"], want)
	}
	posted = nil
	cmd = cardCmd(e)
	cmd.SetArgs([]string{"new", "q", "--predicate", "effect"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "NAME=TEXT") {
		t.Fatalf("missing text: %v", err)
	}
	if posted != nil {
		t.Fatal("invalid predicate reached server")
	}
}

func TestCardShowPrintsPredicates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/comments") {
			w.Write([]byte(`[]`))
			return
		}
		if r.URL.Path == "/api/cards/x" {
			w.Write([]byte(`{"id":"x","preregistration":[]}`))
			return
		}
		w.Write([]byte(`[{"id":"x","number":1,"title":"t","question":"why","predicates":[{"name":"effect","text":"effect > 0.2"}]}]`))
	}))
	defer srv.Close()
	var out bytes.Buffer
	cmd := cardCmd(&env{cfg: Config{Server: srv.URL, Token: "ltk_run", Workspace: "w"}, out: &out})
	cmd.SetArgs([]string{"show", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "why\npre-registered: effect: effect > 0.2 — not decided yet\n") {
		t.Fatalf("%q", out.String())
	}
}

func TestCardShowPrintsBindingOutcomes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cards/x/comments":
			w.Write([]byte(`[]`))
		case "/api/cards/x":
			w.Write([]byte(`{"id":"x","preregistration":[{"name":"effect","text":"effect > 0.2","outcome":"missed","round":1,"run":"x1","values":{"effect":0.12}},
				{"name":"both","text":"a > 0 and b > 0","outcome":"held","round":1,"run":"x1","values":{"b":2,"a":1}},
				{"name":"open","text":"c > 0","outcome":"unconfirmed","reason":"no results run named"}]}`))
		default:
			w.Write([]byte(`[{"id":"x","number":1,"title":"t","question":"why","predicates":[{"name":"effect","text":"effect > 0.2"},
				{"name":"both","text":"a > 0 and b > 0"},{"name":"open","text":"c > 0"},{"name":"later","text":"d > 0"}]}]`))
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	cmd := cardCmd(&env{cfg: Config{Server: srv.URL, Token: "ltk_run", Workspace: "w"}, out: &out})
	cmd.SetArgs([]string{"show", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pre-registered: effect: effect > 0.2 — missed (0.12, run x1)\n",
		"pre-registered: both: a > 0 and b > 0 — held (a=1, b=2, run x1)\n",
		"pre-registered: open: c > 0 — not decided yet\n",
		"pre-registered: later: d > 0 — not decided yet\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in %q", want, out.String())
		}
	}
}
