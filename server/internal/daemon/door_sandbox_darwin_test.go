package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lykeion/server/internal/daemon/envs"
)

// realSandboxDoor is a door whose scripts run under the system's Python in
// the real sandbox, for a home folder with a planted key.
func realSandboxDoor(t *testing.T, l *lykeion, dir string, langs ...string) (*Door, string) {
	t.Helper()
	home := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	must(t, os.WriteFile(filepath.Join(home, ".ssh", "id_rsa"), []byte("PRIVATE KEY"), 0o600))
	if len(langs) == 0 {
		langs = []string{envs.Python}
	}
	d := newRunDoor(t, l, dir, func(d *Door) {
		d.home = home
		d.interp = func(lang string) (string, []string, error) {
			if _, _, err := d.Study.Interpreter(lang); err != nil {
				return "", nil, err
			}
			return realPython(t), nil, nil
		}
	}, langs...)
	return d, home
}

// realPython is the interpreter /usr/bin/python3 stands in for: the shim
// itself keeps a cache in the person's temp folder, which the sandbox
// refuses, and says so on stderr.
func realPython(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("/usr/bin/python3", "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Skip("no python3: ", err)
	}
	return strings.TrimSpace(string(out))
}

func TestAScriptCannotReadTheHomeFolder(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, home := realSandboxDoor(t, l, dir)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte(fmt.Sprintf("print(open(%q).read())\n", filepath.Join(home, ".ssh", "id_rsa"))), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "PRIVATE KEY") || !strings.Contains(got, "Operation not permitted") {
		t.Fatalf("answer %q", got)
	}
	runs, _ := l.recorded()
	if len(runs) != 1 || runs[0]["exit_status"] != float64(1) || !strings.Contains(runs[0]["error"].(string), "Operation not permitted") || runs[0]["packages"] != fakePackages {
		t.Fatalf("runs %v", runs)
	}
}

func TestAScriptWritesOnlyItsOwnFolders(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, home := realSandboxDoor(t, l, dir)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("import os, tempfile\nopen('out.txt', 'w').write('ok')\ntempfile.NamedTemporaryFile(delete=False).write(b'x')\nprint('wrote')\n"), 0o644))
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil || got != "run:x1\nwrote\n" {
		t.Fatalf("%q %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "out.txt")); err != nil || string(b) != "ok" {
		t.Fatalf("out.txt %q %v", b, err)
	}
	probe := filepath.Join(home, "lyk-sandbox-probe")
	must(t, os.WriteFile(filepath.Join(dir, "b.py"), []byte(fmt.Sprintf("open(%q, 'w').write('x')\n", probe)), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "b.py"})
	if err != nil || !strings.Contains(got, "Operation not permitted") {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(probe); err == nil {
		t.Fatal("the script wrote the home folder")
	}
}

func TestAScriptCannotChangeTheCLIsSettings(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, _ := realSandboxDoor(t, l, dir)
	script := "open('data.csv', 'w').write('1')\nprint('data')\nimport os\nos.makedirs('.claude', exist_ok=True)\nopen('.claude/settings.json', 'w').write('{}')\n"
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte(script), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || !strings.HasPrefix(got, "run:x1\ndata\n\n[stderr]\nTraceback") || !strings.Contains(got, "Operation not permitted") {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
		t.Fatal("the script wrote the CLI's settings")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "data.csv")); err != nil || string(b) != "1" {
		t.Fatalf("data.csv %q %v", b, err)
	}
}

func TestAScriptCannotReadTheDoorsToken(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".lyk-mcp.json"), []byte(`{"Authorization":"Bearer DOOR TOKEN"}`), 0o600))
	d, _ := realSandboxDoor(t, l, dir)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("print(open('.lyk-mcp.json').read())\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || strings.Contains(got, "DOOR TOKEN") || !strings.Contains(got, "Operation not permitted") {
		t.Fatalf("%q %v", got, err)
	}
	if runs, _ := l.recorded(); strings.Contains(fmt.Sprint(runs), "DOOR TOKEN") {
		t.Fatal("the token reached the record")
	}
}

// A venv the code broke is made again in the sandbox: a .pth it planted
// runs only there, if at all, and the run goes on.
func TestADamagedVenvIsRebuiltInTheSandbox(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, home := realSandboxDoor(t, l, dir)
	base := filepath.Join(d.Study.Bases.Dir, "python", "bin", "python")
	must(t, os.Remove(base))
	must(t, os.Symlink(realPython(t), base))
	pwned := filepath.Join(home, "pwned")
	for _, site := range []string{filepath.Join(d.Study.Dir, "python", "lib", "site-packages"), filepath.Join(d.Study.Dir, "python", "lib", "python3.9", "site-packages")} {
		must(t, os.MkdirAll(site, 0o755))
		must(t, os.WriteFile(filepath.Join(site, "evil.pth"), []byte("import os; open("+strconv.Quote(pwned)+", 'w').write('x')\n"), 0o644))
	}
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("print('ran')\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || got != "run:x1\nran\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("the planted .pth wrote the home folder")
	}
	if _, err := os.Stat(filepath.Join(d.Study.Dir, "python", "bin", "python")); err != nil {
		t.Fatalf("no venv after the rebuild: %v", err)
	}
}

// The venv is made in the sandbox: what the base's interpreter runs while
// making it can't reach the home folder.
func TestTheVenvIsMadeInTheSandbox(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, home := realSandboxDoor(t, l, dir)
	pwned := filepath.Join(home, "pwned")
	base := filepath.Join(d.Study.Bases.Dir, "python", "bin", "python")
	must(t, os.WriteFile(base, []byte("#!/bin/sh\necho x > "+shq(pwned)+"\n"+strings.TrimPrefix(fakeBasePython, "#!/bin/sh\n")), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("print('ran')\n"), 0o644))
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil || got != "run:x1\nran\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("making the venv wrote the home folder")
	}
}

// Julia's manifest is read in the sandbox: a link the code put in its place
// reads nothing of the home folder into the record.
func TestALinkedManifestReadsNothing(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, home := realSandboxDoor(t, l, dir, envs.Python, envs.Julia)
	must(t, os.MkdirAll(filepath.Join(d.Study.Dir, "julia"), 0o755))
	must(t, os.Symlink(filepath.Join(home, ".ssh", "id_rsa"), filepath.Join(d.Study.Dir, "julia", "Manifest.toml")))
	must(t, os.WriteFile(filepath.Join(dir, "a.jl"), []byte("print('ran')\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.jl"})
	runs, _ := l.recorded()
	if strings.Contains(got+fmt.Sprint(err)+fmt.Sprint(runs), "PRIVATE KEY") {
		t.Fatalf("the key was read: %q %v %v", got, err, runs)
	}
}

// R's add-on folder is made in the sandbox: a link the code put in its
// place leads the daemon nowhere.
func TestALinkedRFolderWritesNothingOutside(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, _ := realSandboxDoor(t, l, dir, envs.Python, envs.R)
	outside := t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(d.Study.Dir, "r")))
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("print('ran')\n"), 0o644))
	useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if es, _ := os.ReadDir(outside); len(es) != 0 {
		t.Fatalf("written through the link: %v", es)
	}
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// The supervisor writes its exit marker from outside the sandbox, so code
// may not put a link where it will write.
func TestAScriptCannotPlantTheExitMarker(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d, home := realSandboxDoor(t, l, dir)
	thesis := filepath.Join(home, "thesis.tex")
	must(t, os.WriteFile(thesis, []byte("THESIS"), 0o600))
	script := "import os\nfor m in ('.agent-exit.tmp', '.agent-exit'):\n    try:\n        os.remove(m)\n    except OSError as e:\n        print(m, e)\n    try:\n        os.symlink(" + strconv.Quote(thesis) + ", m)\n    except OSError as e:\n        print(m, e)\n"
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte(script), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || strings.Count(got, "Operation not permitted") < 2 {
		t.Fatalf("%q %v", got, err)
	}
	for _, m := range []string{".agent-exit.tmp", ".agent-exit"} {
		if fi, err := os.Lstat(filepath.Join(dir, m)); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s is no longer the placeholder: %v", m, err)
		}
	}
}

func TestAScriptInTheSandboxSeesNoSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_planted")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws_planted")
	t.Setenv("PYTHONPATH", "/persons/python")
	t.Setenv("NO_PROXY", "localhost")
	l := newLykeion(t)
	dir := t.TempDir()
	d, _ := realSandboxDoor(t, l, dir)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("import os\nfor k, v in sorted(os.environ.items()): print(k + '=' + v)\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"ghp_planted", "aws_planted", "/persons/python"} {
		if strings.Contains(got, bad) {
			t.Errorf("the script saw %q", bad)
		}
	}
	if !strings.Contains(got, "\nNO_PROXY=localhost\n") || !strings.Contains(got, "\nHOME="+d.tmp) {
		t.Errorf("env %s", got)
	}
}
