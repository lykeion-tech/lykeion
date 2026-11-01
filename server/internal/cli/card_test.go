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

func TestCardNewHasNoStudyVariableFlags(t *testing.T) {
	e := &env{cfg: Config{Server: "http://unused", Token: "ltk_run", Workspace: "w"}, out: &bytes.Buffer{}}
	for _, args := range [][]string{{"new", "--env", "A=1", "q"}, {"new", "--service-env", "evaluator=DATA", "q"}} {
		cmd := cardCmd(e)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || err.Error() != "unknown flag: "+args[1] {
			t.Fatalf("%v: %v", args, err)
		}
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
	cmd.SetArgs([]string{"new", "q", "--service", "evaluator=" + folder, "--service-start", "evaluator=python server.py"})
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

func TestCardNewHasNoPredicateFlag(t *testing.T) {
	e := &env{cfg: Config{Server: "http://unused", Token: "ltk_run", Workspace: "w"}, out: &bytes.Buffer{}}
	cmd := cardCmd(e)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"new", "--predicate", "effect=effect > 0", "q"})
	if err := cmd.Execute(); err == nil || err.Error() != "unknown flag: --predicate" {
		t.Fatalf("--predicate: %v", err)
	}
}

func TestCardNewSendsAnAgent(t *testing.T) {
	const live, gone = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	var posted map[string]any
	listed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/api/agents" {
			listed++
			w.Write([]byte(`[{"id":"` + gone + `","name":"solo","archived_at":"2026-01-01T00:00:00Z"},{"id":"` + live + `","name":"solo","archived_at":null}]`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"id":"x","number":1,"title":"q"}`))
	}))
	defer srv.Close()
	e := &env{cfg: Config{Server: srv.URL, Token: "ltk_run", Workspace: "w"}, out: &bytes.Buffer{}}
	send := func(args ...string) error {
		posted = nil
		cmd := cardCmd(e)
		cmd.SetArgs(append([]string{"new"}, args...))
		return cmd.Execute()
	}
	if err := send("--agent", "solo", "q"); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"type": "agent", "id": live}; !reflect.DeepEqual(posted["assignee"], want) {
		t.Fatalf("posted assignee %#v, want %#v", posted["assignee"], want)
	}
	listed = 0
	if err := send("--agent", gone, "q"); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"type": "agent", "id": gone}; listed != 0 || !reflect.DeepEqual(posted["assignee"], want) {
		t.Fatalf("an id was listed %d times and posted %#v", listed, posted["assignee"])
	}
	if err := send("--agent", "x", "q"); err == nil || err.Error() != `no agent named "x"` {
		t.Fatalf("unknown name: %v", err)
	}
	if posted != nil {
		t.Fatal("an unknown agent reached the server")
	}
}

func TestCardNewHasNoFollowsFromFlag(t *testing.T) {
	e := &env{cfg: Config{Server: "http://unused", Token: "ltk_run", Workspace: "w"}, out: &bytes.Buffer{}}
	cmd := cardCmd(e)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"new", "--follows-from", "3", "q"})
	if err := cmd.Execute(); err == nil || err.Error() != "unknown flag: --follows-from" {
		t.Fatalf("%v", err)
	}
}
