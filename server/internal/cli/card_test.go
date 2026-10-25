package cli

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
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
