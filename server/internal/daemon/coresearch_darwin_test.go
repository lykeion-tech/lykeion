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

func TestCoresearchWriterGuardCannotBeResetByCLI(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skip(err)
	}
	f := newCoreFixture(t)
	h := f.d.chats[f.work.ChatID]
	must(t, f.d.markChatWriter(h))
	path := f.d.chatWriterRecord(h)
	command := f.d.cliSandbox(h.dir, nil, false)
	for _, script := range []string{
		`printf false > "$1"`,
		`rm "$1"`,
		`printf false > temp; mv temp "$1"`,
		`mv "$2" moved-state`,
	} {
		cmd, err := command(t.Context(), agent.CLIPaths{}, "/bin/sh", "-c", script, "sh", path, f.d.cfg.StateDir)
		must(t, err)
		cmd.Dir = h.dir
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("writer guard tampering allowed: %s %s", script, output)
		}
		data, err := os.ReadFile(path)
		must(t, err)
		if !strings.Contains(string(data), `"writer_launched":true`) {
			t.Fatalf("writer guard changed: %s", data)
		}
	}
}

func TestCoresearchClaimedPythonSelectsRealInterpreter(t *testing.T) {
	if os.Getenv("LYK_TEST_ENVS") != "1" {
		t.Skip("set LYK_TEST_ENVS=1 for the real cached-version test")
	}
	python := os.Getenv("LYK_TEST_CELL_PYTHON")
	if python == "" {
		t.Skip("set LYK_TEST_CELL_PYTHON to a read-only Jupyter interpreter")
	}
	if err := sandbox.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	actual, err := exec.Command(python, "-I", "-c", `import sys; print('%d.%d' % sys.version_info[:2])`).Output()
	must(t, err)
	version := strings.TrimSpace(string(actual))
	f := newCoreFixture(t)
	study := fakeStudy(t, "python")
	h := f.d.chats[f.work.ChatID]
	h.study = study
	for _, name := range []string{"python", "conda"} {
		dir := filepath.Join(study.Dir, name, "bin")
		must(t, os.MkdirAll(dir, 0700))
		must(t, os.Symlink(python, filepath.Join(dir, "python")))
	}
	must(t, os.WriteFile(filepath.Join(study.Dir, "conda", ".lyk-ready"), nil, 0600))
	specDir := filepath.Join(study.Dir, "conda", "share", "jupyter", "kernels", "selected")
	must(t, os.MkdirAll(specDir, 0700))
	selected := filepath.Join(study.Dir, "python", "bin", "python")
	spec := map[string]any{"argv": []string{selected, "-m", "ipykernel_launcher", "-f", "{connection_file}"}, "display_name": "Selected", "language": "python"}
	must(t, os.WriteFile(filepath.Join(specDir, "kernel.json"), mustJSON(spec), 0600))
	tmp, bridge := t.TempDir(), t.TempDir()
	p := codePolicy(t.TempDir(), h.dir, study, tmp, []string{f.d.cfg.StateDir}, nil)
	p.Read = append(p.Read, bridge, filepath.Dir(filepath.Dir(python)))
	p.ReadOnly = append(p.ReadOnly, filepath.Join(h.dir, ".lyk"))
	command := func(name string, args ...string) (*exec.Cmd, error) {
		return p.Command(context.Background(), name, args...)
	}
	start := func() *kernels.Sessions {
		view := h.study.ForRun()
		view.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
			cmd, err := p.Command(ctx, name, args...)
			if err != nil {
				return nil, err
			}
			cmd.Env = append(corePersonEnv(), env...)
			cmd.Dir = dir
			return cmd.CombinedOutput()
		}
		env, err := view.Environment(t.Context())
		must(t, err)
		env = coreJupyterEnv(view, env)
		env = append(append(corePersonEnv(), env...), coreTmpEnv(tmp)...)
		interpreter, _, err := view.Interpreter("python")
		must(t, err)
		if interpreter != selected {
			t.Fatalf("wrong selected interpreter %q", interpreter)
		}
		sessions, err := kernels.Start(t.Context(), command, interpreter, env, h.dir, bridge)
		must(t, err)
		h.sessions = sessions
		h.kernelPython, h.kernelGeneration = view.RecordedPython(), view.PythonGeneration()
		return sessions
	}
	must(t, f.d.markChatWriter(h))
	old := start()
	result, err := old.CellWithID(t.Context(), "before-pin", "selected", "old_variable = 1")
	must(t, err)
	if !result.OK {
		t.Fatal(result)
	}
	updated := f.work
	updated.PythonVersion = &version
	_, err = f.d.chat(t.Context(), updated)
	must(t, err)
	h.mu.Lock()
	err = f.d.retireChangedKernel(t.Context(), h)
	h.mu.Unlock()
	must(t, err)
	if h.sessions != nil {
		t.Fatal("old real session was not retired")
	}
	next := start()
	code := `import sys, os
assert 'old_variable' not in globals()
assert '%d.%d' % sys.version_info[:2] == ` + string(mustJSON(version)) + `
assert os.path.realpath(sys.executable) == os.path.realpath(` + string(mustJSON(python)) + `)
try:
    open(` + string(mustJSON(f.d.chatWriterRecord(h))) + `, 'w').write('false')
except PermissionError:
    pass
else:
    raise AssertionError('writer guard was writable')`
	result, err = next.CellWithID(t.Context(), "after-pin", "selected", code)
	must(t, err)
	if !result.OK {
		t.Fatal(result)
	}
	if h.study.PythonVersion != version || h.kernelPython != version {
		t.Fatal("real session lost claimed selection")
	}
}
