package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func coresearchServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ltk_run" || r.Header.Get("X-Workspace-ID") != "" {
			t.Errorf("wrong credentials: %v", r.Header)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/run/coresearch/") {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := inRun(t, srv)
	t.Setenv("LYK_CONFIG", filepath.Join(dir, ".lyk/config.json"))
	writeFile(t, filepath.Join(dir, ".lyk/config.json"), "not person config")
	old := coresearchPollInterval
	coresearchPollInterval = time.Millisecond
	t.Cleanup(func() { coresearchPollInterval = old })
	return dir
}
func coresearchExecute(input string, args ...string) (string, error) {
	var out strings.Builder
	cmd := Root(strings.NewReader(input), &out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}
func TestCellRunPrintsOutputs(t *testing.T) {
	gets := 0
	coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["kernel"] != "custom" || in["source"] != "print(42)" {
				t.Errorf("body %v", in)
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"c1","status":"queued"}`)
			return
		}
		if r.URL.Path != "/api/run/coresearch/cells/c1" {
			t.Errorf("poll %s", r.URL.Path)
		}
		gets++
		if gets == 1 {
			fmt.Fprint(w, `{"id":"c1","status":"running"}`)
			return
		}
		fmt.Fprint(w, `{"id":"c1","status":"ok","outputs":[{"type":"stream","name":"stdout","text":"hello\n"},{"type":"text","text":"42\n"},{"type":"image","mime":"image/png","data":"YWJjZA=="},{"type":"error","ename":"ValueError","evalue":"bad value","traceback":["Traceback","bad value"]}]}`)
	})
	out, err := coresearchExecute("print(42)", "cell", "run", "--kernel", "custom")
	if err != nil || out != "hello\n42\n[image/png 4 bytes: cell c1]\nTraceback\nbad value\n" || gets != 2 {
		t.Fatalf("out=%q err=%v gets=%d", out, err, gets)
	}
}
func TestCellRunExitCodes(t *testing.T) {
	for _, tt := range []struct {
		s    string
		code int
	}{{"ok", 0}, {"error", 1}, {"interrupted", 1}, {"cancelled", 1}} {
		t.Run(tt.s, func(t *testing.T) {
			coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"c1","status":"queued"}`)
					return
				}
				fmt.Fprintf(w, `{"id":"c1","status":%q,"failure_reason":"stopped"}`, tt.s)
			})
			_, err := coresearchExecute("x", "cell", "run")
			if ExitCode(err) != tt.code {
				t.Fatalf("err=%v code=%d", err, ExitCode(err))
			}
		})
	}
}
func TestCellRunGivesUpWhenNobodyClaims(t *testing.T) {
	old := coresearchCellWait
	coresearchCellWait = 5 * time.Millisecond
	t.Cleanup(func() { coresearchCellWait = old })
	gets := 0
	coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(201)
		} else {
			gets++
		}
		fmt.Fprint(w, `{"id":"c1","status":"queued"}`)
	})
	_, err := coresearchExecute("x", "cell", "run")
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "the chat's machine did not run the cell") || gets < 1 {
		t.Fatalf("err=%v gets=%d", err, gets)
	}
}
func TestCellRunLimitsAndPollingErrors(t *testing.T) {
	requests := 0
	coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method == "POST" {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"c1","status":"queued"}`)
			return
		}
		http.Error(w, `{"error":"membership revoked"}`, 404)
	})
	if _, err := coresearchExecute(strings.Repeat("x", 256<<10+1), "cell", "run"); err == nil || requests != 0 {
		t.Fatalf("oversized err=%v requests=%d", err, requests)
	}
	if _, err := coresearchExecute(strings.Repeat("x", 256<<10), "cell", "run"); err == nil || !strings.Contains(err.Error(), "membership revoked") || requests != 2 {
		t.Fatalf("poll err=%v requests=%d", err, requests)
	}
}
func TestRunWaitsAndPrintsResults(t *testing.T) {
	gets := 0
	coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in struct {
				Script string
				Inputs []string
				Rerun  string `json:"rerun_of_key"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if in.Script != "analysis.py" || strings.Join(in.Inputs, ",") != "a.csv,b.csv" || in.Rerun != "R3" {
				t.Errorf("body %+v", in)
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"key":"R4","status":"queued"}`)
			return
		}
		if r.URL.Path != "/api/run/coresearch/runs/R4" {
			t.Errorf("must use immutable run outputs: %s", r.URL.Path)
		}
		gets++
		if gets == 1 {
			fmt.Fprint(w, `{"key":"R4","status":"running"}`)
			return
		}
		fmt.Fprint(w, `{"key":"R4","status":"ok","facts":{"seed":7,"outputs":{"results/a.png":"hash"}},"outputs":[{"path":"results/a.png","file_id":"f1","n":1,"sha256":"hash","size":4}]}`)
	})
	out, err := coresearchExecute("", "run", "analysis.py", "--in", "a.csv", "--in", "b.csv", "--rerun", "R3")
	if err != nil || !strings.Contains(out, "R4 ok\n") || !strings.Contains(out, `"seed":7`) || !strings.Contains(out, "results/a.png v1\n") || gets != 2 {
		t.Fatalf("out=%q err=%v gets=%d", out, err, gets)
	}
}
func TestFileSaveUsesFillBase(t *testing.T) {
	var bases, forces []string
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/run/coresearch/files" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
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
		if r.FormValue("path") != "src/a.py" || string(b) != "print(1)" {
			t.Errorf("upload %v %q", r.Form, b)
		}
		bases = append(bases, r.FormValue("base"))
		forces = append(forces, r.FormValue("force"))
		w.WriteHeader(201)
		fmt.Fprint(w, `{"n":8,"sha256":"newhash"}`)
	})
	writeFile(t, filepath.Join(dir, "src/a.py"), "print(1)")
	writeFile(t, filepath.Join(dir, ".lyk/fill.json"), `{"src/a.py":{"n":7,"sha256":"oldhash"},"other":{"n":2,"sha256":"keep"}}`)
	for _, args := range [][]string{{}, {}, {"--base", "3", "--force"}} {
		if _, err := coresearchExecute("", append([]string{"file", "save", "src/a.py"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(bases, ",") != "7,8,3" || forces[0] == "true" || forces[2] != "true" {
		t.Fatalf("base=%v force=%v", bases, forces)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".lyk/fill.json"))
	var fill map[string]struct {
		N   int
		SHA string `json:"sha256"`
	}
	json.Unmarshal(b, &fill)
	if fill["src/a.py"].N != 8 || fill["other"].SHA != "keep" {
		t.Fatalf("fill %s", b)
	}
}
func TestFileSaveStaleExits3(t *testing.T) {
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(409)
		fmt.Fprint(w, `{"error":"version 1 is not the latest (2 is); save with force to override"}`)
	})
	writeFile(t, filepath.Join(dir, "a.py"), "x")
	_, err := coresearchExecute("", "file", "save", "a.py")
	if ExitCode(err) != 3 || !strings.Contains(err.Error(), "version 1 is not the latest") {
		t.Fatalf("err=%v", err)
	}
}
func TestFileSaveRefusesOutsideFolder(t *testing.T) {
	requests := 0
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) { requests++; http.Error(w, "unexpected", 500) })
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), "secret")
	for link, target := range map[string]string{"outside": outside, "inside": filepath.Join(dir, ".lyk"), "link": filepath.Join(outside, "secret")} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"../x", filepath.Join(outside, "secret"), "outside/secret", "inside/config.json", "link", ".lyk/config.json", ".claude/settings", ".codex/x", ".cursor/x", ".agents/x", ".cli-home/x", ".mcp.json", ".lyk-mcp.json", ".venv/x", ".lyk-skills/x", ".no-lyk-config", "a/../x"} {
		for _, verb := range []string{"save", "get"} {
			if _, err := coresearchExecute("", "file", verb, p); err == nil {
				t.Errorf("%s %s accepted", verb, p)
			}
		}
	}
	if requests != 0 {
		t.Fatalf("sent %d forbidden requests", requests)
	}
}
func TestFileGetWritesIntoChatFolder(t *testing.T) {
	var base string
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			r.ParseMultipartForm(2 << 20)
			defer r.MultipartForm.RemoveAll()
			base = r.FormValue("base")
			w.WriteHeader(201)
			fmt.Fprint(w, `{"n":4,"sha256":"next"}`)
			return
		}
		if r.URL.Path == "/api/run/coresearch/files" {
			fmt.Fprint(w, `[{"path":"nested/a.txt","n":3,"sha256":"7eab3a760271583339d18849cc54c4ca3f8e30cdc650f05a39c2dc218b0715ab","size":11}]`)
			return
		}
		if r.URL.Query().Get("path") != "nested/a.txt" || r.URL.Query().Get("n") != "3" {
			t.Errorf("wrong download %s", r.URL)
		}
		fmt.Fprint(w, "saved bytes")
	})
	t.Chdir(t.TempDir())
	if _, err := coresearchExecute("", "file", "get", "nested/a.txt", "--version", "3"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "nested/a.txt"))
	if err != nil || string(b) != "saved bytes" {
		t.Fatalf("bytes=%q err=%v", b, err)
	}
	if _, err := coresearchExecute("", "file", "save", "nested/a.txt"); err != nil {
		t.Fatal(err)
	}
	if base != "3" {
		t.Fatalf("subsequent base=%q", base)
	}
}
func TestFileGetFailurePreservesExistingFile(t *testing.T) {
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"error":"gone"}`, 404) })
	writeFile(t, filepath.Join(dir, "a.txt"), "original")
	if _, err := coresearchExecute("", "file", "get", "a.txt"); err == nil {
		t.Fatal("missing error")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "original" {
		t.Fatalf("file replaced: %q", b)
	}
}
func TestEnvSetUpWaits(t *testing.T) {
	for _, python := range []string{"", "3.12"} {
		t.Run(python, func(t *testing.T) {
			gets := 0
			coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					var in map[string]any
					json.NewDecoder(r.Body).Decode(&in)
					if python == "" {
						if _, ok := in["python"]; ok {
							t.Errorf("python must be omitted: %v", in)
						}
					} else if in["python"] != python {
						t.Errorf("python %v", in)
					}
					b, _ := json.Marshal(in["conda"])
					if string(b) != `["numpy","scipy"]` {
						t.Errorf("conda %s", b)
					}
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"s1","status":"queued"}`)
					return
				}
				if r.URL.Path != "/api/run/coresearch/environment/s1" {
					t.Errorf("poll %s", r.URL.Path)
				}
				gets++
				if gets == 1 {
					fmt.Fprint(w, `{"id":"s1","status":"running"}`)
					return
				}
				fmt.Fprint(w, `{"id":"s1","status":"ok","log":"installing\nready\n"}`)
			})
			args := []string{"env", "set-up", "--conda", "numpy", "--conda", "scipy"}
			if python != "" {
				args = append(args, "--python", python)
			}
			out, err := coresearchExecute("", args...)
			if err != nil || !strings.Contains(out, "ready\n") || gets != 2 {
				t.Fatalf("out=%q err=%v gets=%d", out, err, gets)
			}
		})
	}
}
func TestCoresearchRequiresRunTokenAndExactConfigLayout(t *testing.T) {
	requests := 0
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) { requests++; http.Error(w, "unexpected", 500) })
	writeFile(t, filepath.Join(dir, "a.py"), "x")
	for _, token := range []string{"", "person-token"} {
		t.Setenv("LYK_TOKEN", token)
		if _, err := coresearchExecute("x", "cell", "run"); err == nil {
			t.Errorf("accepted token %q", token)
		}
	}
	t.Setenv("LYK_TOKEN", "ltk_run")
	for _, p := range []string{filepath.Join(dir, "config.json"), filepath.Join(dir, ".lyk/wrong.json"), ""} {
		t.Setenv("LYK_CONFIG", p)
		if _, err := coresearchExecute("", "file", "save", "a.py"); err == nil {
			t.Errorf("accepted config %q", p)
		}
	}
	if requests != 0 {
		t.Fatalf("made %d requests", requests)
	}
}
func TestCoresearchPollingHonorsContext(t *testing.T) {
	coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"c1","status":"queued"}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out strings.Builder
	cmd := Root(strings.NewReader("x"), &out)
	cmd.SetArgs([]string{"cell", "run"})
	cmd.SetContext(ctx)
	if err := cmd.Execute(); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestCoresearchRunAndSetupFailuresAndTimeouts(t *testing.T) {
	for _, kind := range []string{"run", "env"} {
		for _, status := range []string{"failed", "cancelled", "running", "queued"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				oldRun, oldSetup := coresearchRunWait, coresearchSetupWait
				coresearchRunWait = 5 * time.Millisecond
				coresearchSetupWait = 5 * time.Millisecond
				t.Cleanup(func() { coresearchRunWait = oldRun; coresearchSetupWait = oldSetup })
				polls := 0
				coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" {
						w.WriteHeader(201)
						fmt.Fprint(w, `{"id":"s1","key":"R1","status":"queued"}`)
						return
					}
					polls++
					fmt.Fprintf(w, `{"id":"s1","key":"R1","status":%q,"log":"failed install\n","failure_reason":"stopped"}`, status)
				})
				args := []string{"run", "a.py"}
				if kind == "env" {
					args = []string{"env", "set-up"}
				}
				out, err := coresearchExecute("", args...)
				want := 1
				if status == "queued" {
					want = 2
				}
				if ExitCode(err) != want || polls < 1 {
					t.Fatalf("err=%v polls=%d", err, polls)
				}
				if kind == "env" && status == "failed" && !strings.Contains(out, "failed install") {
					t.Fatalf("missing log %q", out)
				}
			})
		}
	}
}
func TestFileSaveMalformedManifestAndSymlinkStateRefused(t *testing.T) {
	requests := 0
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) { requests++; http.Error(w, "unexpected", 500) })
	writeFile(t, filepath.Join(dir, "a.py"), "x")
	writeFile(t, filepath.Join(dir, ".lyk/fill.json"), `{"a.py":`)
	if _, err := coresearchExecute("", "file", "save", "a.py"); err == nil {
		t.Fatal("malformed manifest accepted")
	}
	if err := os.Remove(filepath.Join(dir, ".lyk/fill.json")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "manifest")
	writeFile(t, target, `{}`)
	for _, state := range []string{"fill.json", "fill.lock"} {
		full := filepath.Join(dir, ".lyk", state)
		os.Remove(full)
		if err := os.Symlink(target, full); err != nil {
			t.Fatal(err)
		}
		if _, err := coresearchExecute("", "file", "save", "a.py"); err == nil {
			t.Errorf("symlink %s accepted", state)
		}
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 0 {
		t.Fatalf("made %d requests", requests)
	}
}
func TestFileGetPinsHeadAndPreservesOnHashMismatch(t *testing.T) {
	dir := coresearchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/run/coresearch/files" {
			fmt.Fprint(w, `[{"path":"a.txt","n":7,"sha256":"incorrect","size":3}]`)
			return
		}
		if r.URL.Query().Get("n") != "7" {
			t.Errorf("download was not pinned: %s", r.URL)
		}
		fmt.Fprint(w, "new")
	})
	writeFile(t, filepath.Join(dir, "a.txt"), "original")
	if _, err := coresearchExecute("", "file", "get", "a.txt"); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err=%v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "original" {
		t.Fatalf("file replaced: %q", b)
	}
}
