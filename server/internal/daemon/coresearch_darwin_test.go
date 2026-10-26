package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lykeion/server/internal/coresearch/localfiles"
	"lykeion/server/internal/daemon/kernels"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func TestCoreManifestChild(t *testing.T) {
	if os.Getenv("CORE_MANIFEST_CHILD") != "1" {
		return
	}
	dir, e := os.Getwd()
	must(t, e)
	f, e := localfiles.Open(dir)
	must(t, e)
	defer f.Close()
	m, e := f.LockManifest(t.Context())
	must(t, e)
	defer m.Close()
	m.Entries["saved.txt"] = localfiles.Pin{N: 2, SHA256: strings.Repeat("a", 64)}
	must(t, m.Save())
}
func TestCoresearchCLIAtomicManifestSave(t *testing.T) {
	if e := sandbox.Check(t.Context()); e != nil {
		t.Skip(e)
	}
	f := newCoreFixture(t)
	exe, e := os.Executable()
	must(t, e)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := f.d.cliSandbox(f.dir(), nil, false)
	cmd, e := command(ctx, agent.CLIPaths{}, exe, "-test.run=^TestCoreManifestChild$")
	must(t, e)
	cmd.Dir = f.dir()
	cmd.Env = append(os.Environ(), "CORE_MANIFEST_CHILD=1")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("atomic manifest save: %v %s", e, out)
	}
	data, e := os.ReadFile(filepath.Join(f.dir(), ".lyk/fill.json"))
	must(t, e)
	var pins map[string]localfiles.Pin
	must(t, json.Unmarshal(data, &pins))
	if pins["saved.txt"].N != 2 {
		t.Fatal(pins)
	}
	for _, script := range []string{
		`printf bad > .lyk/config.json`,
		`rm .lyk/config.json`,
		`printf bad > .lyk/temp; mv .lyk/temp .lyk/config.json`,
		`rm .lyk/fill.lock`,
		`mv .lyk/fill.lock .lyk/other`,
		`printf bad > .lyk/temp; mv .lyk/temp .lyk/fill.lock`,
		`mv .lyk moved`,
	} {
		cmd, e := command(ctx, agent.CLIPaths{}, "/bin/sh", "-c", script)
		must(t, e)
		cmd.Dir = f.dir()
		out, e := cmd.CombinedOutput()
		if e == nil {
			t.Fatalf("metadata escape allowed %q: %s", script, out)
		}
	}
}

func TestCellKernelCannotWriteHome(t *testing.T) {
	if os.Getenv("LYK_TEST_ENVS") != "1" {
		t.Skip("set LYK_TEST_ENVS=1 for the real Jupyter sandbox test")
	}
	python := os.Getenv("LYK_TEST_CELL_PYTHON")
	if python == "" {
		t.Skip("set LYK_TEST_CELL_PYTHON to a read-only Jupyter interpreter")
	}
	if e := sandbox.Check(t.Context()); e != nil {
		t.Fatal(e)
	}
	f := newCoreFixture(t)
	study := fakeStudy(t, "python")
	h := f.d.chats[f.work.ChatID]
	tmp := t.TempDir()
	bridge := t.TempDir()
	home := t.TempDir()
	secret := filepath.Join(home, "personal.txt")
	must(t, os.WriteFile(secret, []byte("private"), 0600))
	specDir := filepath.Join(study.Dir, "python", "share", "jupyter", "kernels", "project-python")
	must(t, os.MkdirAll(specDir, 0700))
	spec, _ := json.Marshal(map[string]any{"argv": []string{python, "-m", "ipykernel_launcher", "-f", "{connection_file}"}, "display_name": "Project Python", "language": "python"})
	must(t, os.WriteFile(filepath.Join(specDir, "kernel.json"), spec, 0600))
	p := codePolicy(home, h.dir, study, tmp, nil, nil)
	p.Read = append(p.Read, bridge, filepath.Dir(filepath.Dir(python)))
	p.ReadOnly = append(p.ReadOnly, filepath.Join(h.dir, ".lyk"))
	command := func(name string, args ...string) (*exec.Cmd, error) {
		return p.Command(context.Background(), name, args...)
	}
	baseJupyter := filepath.Join(study.Bases.Dir, "python", "share", "jupyter")
	baseSpec := filepath.Join(baseJupyter, "kernels", "project-python")
	must(t, os.MkdirAll(baseSpec, 0700))
	must(t, os.WriteFile(filepath.Join(baseSpec, "kernel.json"), []byte(`{"argv":["/bin/false","{connection_file}"],"display_name":"Wrong base","language":"python"}`), 0600))
	env := append(corePersonEnv(), coreTmpEnv(tmp)...)
	env = append(env, "JUPYTER_PATH="+baseJupyter)
	env = coreJupyterEnv(study, env)
	sessions, e := kernels.Start(t.Context(), command, python, env, h.dir, bridge)
	must(t, e)
	defer sessions.Close()
	code := `import os,sys
paths = ` + string(mustJSON([]string{secret, filepath.Join(h.dir, ".lyk/config.json"), filepath.Join(h.dir, ".lyk/fill.json"), filepath.Join(h.dir, ".lyk/fill.lock")})) + `
for p in paths:
    try:
        open(p, 'w').write('bad')
    except PermissionError:
        pass
    else:
        raise AssertionError('write permitted: ' + p)
assert not any(k.startswith('LYK_') for k in os.environ)
print('protected')`
	code += "\nassert os.path.realpath(sys.executable) == os.path.realpath(" + string(mustJSON(python)) + ")"
	result, e := sessions.CellWithID(t.Context(), "server-cell-id", "project-python", code)
	must(t, e)
	if !result.OK {
		t.Fatalf("real cell: %+v", result)
	}
	b, e := os.ReadFile(secret)
	must(t, e)
	if string(b) != "private" {
		t.Fatal("home mutated")
	}
}
func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

func TestCoresearchCLIFileSaveInsideSandbox(t *testing.T) {
	bin := os.Getenv("LYK_TEST_CLI")
	if bin == "" {
		t.Skip("set LYK_TEST_CLI to a built lyk binary")
	}
	f := newCoreFixture(t)
	must(t, os.WriteFile(filepath.Join(f.dir(), "saved.txt"), []byte("saved science"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/run/coresearch/files" || r.Header.Get("Authorization") != "Bearer ltk_scoped" {
			http.Error(w, "unexpected request", 400)
			return
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("path") != "saved.txt" || r.FormValue("base") != "0" {
			t.Error("wrong saved path/base")
			w.WriteHeader(400)
			return
		}
		file, _, e := r.FormFile("file")
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		data, e := io.ReadAll(file)
		if e != nil || string(data) != "saved science" {
			t.Error("wrong file bytes")
			w.WriteHeader(400)
			return
		}
		sum := sha256.Sum256(data)
		json.NewEncoder(w).Encode(localfiles.Pin{N: 2, SHA256: hex.EncodeToString(sum[:])})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, e := f.d.cliSandbox(f.dir(), nil, false)(ctx, agent.CLIPaths{}, bin, "file", "save", "saved.txt")
	must(t, e)
	cmd.Dir = f.dir()
	cmd.Env = append(os.Environ(), "LYK_TOKEN=ltk_scoped", "LYK_SERVER="+server.URL, "LYK_CONFIG="+filepath.Join(f.dir(), ".lyk/config.json"))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual lyk file save: %v %s", e, out)
	}
	if !strings.Contains(string(out), "saved.txt v2") {
		t.Fatal(string(out))
	}
	folder, e := localfiles.Open(f.dir())
	must(t, e)
	defer folder.Close()
	m, e := folder.LockManifest(t.Context())
	must(t, e)
	defer m.Close()
	if m.Entries["saved.txt"].N != 2 {
		t.Fatal(m.Entries)
	}
}
