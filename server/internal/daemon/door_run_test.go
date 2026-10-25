package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

// The fake interpreter identifies itself by its bridge command, since daemon
// settings must not survive the production environment filter.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "-I" && strings.HasPrefix(filepath.Base(os.Args[2]), "lyk-bridge-") {
		stubBridge()
		os.Exit(0)
	}
}

// stubBridge stands in for the session bridge: it answers each exec with the
// session's cell count and the code, so two cells in one session read 1 and
// 2, and "fail" answers as a cell that raised. A restart sets the count back
// to 0.
func stubBridge() {
	counts := map[string]int{}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID       int    `json:"id"`
			Op       string `json:"op"`
			Code     string `json:"code"`
			Language string `json:"language"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.Op == "shutdown" {
			return
		}
		if req.Op == "restart" {
			counts[req.Language] = 0
			b, _ := json.Marshal(map[string]any{"id": req.ID, "ok": true, "restarted": true})
			os.Stdout.Write(append(b, '\n'))
			continue
		}
		counts[req.Language]++
		r := map[string]any{"id": req.ID, "stdout": fmt.Sprintf("%d ran %s", counts[req.Language], req.Code), "ok": true}
		if strings.HasPrefix(req.Code, "env:") {
			r["stdout"] = os.Getenv(strings.TrimPrefix(req.Code, "env:"))
		}
		if req.Code == "fail" {
			r = map[string]any{"id": req.ID, "error": "boom", "ok": false}
		}
		b, _ := json.Marshal(r)
		os.Stdout.Write(append(b, '\n'))
	}
}

// fakeStudy is a study over bases marked ready without building them. The
// Python base's interpreter is a shell script that, run as -m venv, makes a
// venv whose interpreter answers pip freeze with one add-on package and
// leaves python.ran beside itself.
func fakeStudy(t *testing.T, langs ...string) *envs.Study {
	t.Helper()
	run := func(_ context.Context, _ []string, _, name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
	b := &envs.Bases{Dir: t.TempDir(), Run: run}
	for _, lang := range langs {
		dir := filepath.Join(b.Dir, lang)
		if lang == envs.Julia {
			dir = filepath.Join(b.Dir, "julia", "base")
		}
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "lock.txt"), []byte(lang+"-lock\n"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "ready"), nil, 0o644))
	}
	if slices.Contains(langs, envs.Python) {
		must(t, os.MkdirAll(filepath.Join(b.Dir, "python", "bin"), 0o755))
		must(t, os.WriteFile(filepath.Join(b.Dir, "python", "bin", "python"), []byte(fakeBasePython), 0o755))
	}
	return envs.ForStudy(b, t.TempDir())
}

// fakeBasePython makes the venv named last on its command line.
const fakeBasePython = `#!/bin/sh
[ "$2" = venv ] || exit 3
for v; do :; done
mkdir -p "$v/bin" || exit 4
printf '#!/bin/sh\ntouch "$0.ran"\necho requests==2.32.0\n' > "$v/bin/python" && chmod +x "$v/bin/python"
`

const fakePackages = "# python base\npython-lock\n\n# python study\nrequests==2.32.0\n"

// runDoor is a door over a study with a ready Python base whose scripts run
// under /bin/sh and whose session bridge is the test binary.
// The door's sandbox is a stand-in that runs the command as given in a
// session of its own, as the real one does, and notes each command line and
// policy in sandboxed.
func runDoor(t *testing.T, l *lykeion, dir string, langs ...string) *Door {
	t.Helper()
	d := newRunDoor(t, l, dir, func(d *Door) { d.wrap = stubSandbox(&sandboxed{}) }, langs...)
	return d
}

type sandboxed struct {
	mu       sync.Mutex
	commands [][]string
	policies []sandbox.Policy
}

func (s *sandboxed) seen() ([][]string, []sandbox.Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.commands), slices.Clone(s.policies)
}

func stubSandbox(s *sandboxed) func(context.Context, sandbox.Policy, string, ...string) (*exec.Cmd, error) {
	return func(ctx context.Context, p sandbox.Policy, name string, args ...string) (*exec.Cmd, error) {
		s.mu.Lock()
		s.commands = append(s.commands, append([]string{name}, args...))
		s.policies = append(s.policies, p)
		s.mu.Unlock()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd, nil
	}
}

// newRunDoor starts a door over a fake study after setup, which may set what
// Start reads.
func newRunDoor(t *testing.T, l *lykeion, dir string, setup func(*Door), langs ...string) *Door {
	t.Helper()
	d := &Door{Server: l.URL, Sources: l.URL + "/mcp/sources", RunToken: "ltk_run", CallToken: "ltk_call",
		Child: func() agent.ExecOptions {
			return agent.ExecOptions{Dir: dir, MCPToken: "ltk_run", Env: []string{"LYK_TOKEN=ltk_run", "LYK_DOOR_TOKEN=secret"}}
		}}
	d.Study = fakeStudy(t, langs...)
	d.interp = func(lang string) (string, []string, error) {
		if _, _, err := d.Study.Interpreter(lang); err != nil {
			return "", nil, err
		}
		if lang == envs.Python && os.Getenv("LYK_KERNEL_STUB") == "1" {
			return os.Args[0], nil, nil
		}
		return "/bin/sh", nil, nil
	}
	setup(d)
	must(t, d.Start())
	t.Cleanup(func() { d.Close() })
	return d
}

// toolTimeout is how long useTool waits for a tool's answer.
var toolTimeout = 20 * time.Second

// useTool calls one of the door's tools as the CLI would and answers its text.
func useTool(t *testing.T, d *Door, name string, args map[string]any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	c := mcp.NewClient(&mcp.Implementation{Name: "cli", Version: "0"}, nil)
	s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: d.URL, HTTPClient: &http.Client{Transport: bearer(d.Token)}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return "", err
	}
	defer s.Close()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", errors.New(text(res))
	}
	return text(res), nil
}

// recorded copies the run bodies the fake server kept and the length of every
// body it received.
func (l *lykeion) recorded() ([]map[string]any, []int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.runs), slices.Clone(l.runSizes)
}

func TestRunScriptRecordsTheScriptItsOutputAndPackages(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	script := "echo 42\necho warn >&2\n"
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte(script), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "run:x1\n42\n\n[stderr]\nwarn\n" {
		t.Fatalf("answer %q", got)
	}
	runs, _ := l.recorded()
	if len(runs) != 1 {
		t.Fatalf("%d runs posted", len(runs))
	}
	b := runs[0]
	want := map[string]any{"tool": "run_script", "language": "python", "path": "a.py", "script": script, "output": "42\n", "error": "warn\n", "exit_status": float64(0), "packages": fakePackages}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s = %#v, want %#v", k, b[k], v)
		}
	}
}

func TestRunScriptRecordsAFailingExitStatus(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("exit 3\n"), 0o644))
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil || got != "run:x1\n" {
		t.Fatalf("%q %v", got, err)
	}
	if runs, _ := l.recorded(); runs[0]["exit_status"] != float64(3) {
		t.Fatalf("exit status %v", runs[0]["exit_status"])
	}
}

// Bytes that are not UTF-8 reach the record and the answer as U+FFFD, the
// way JSON encoding writes them, and the run is recorded as usual.
func TestOutputThatIsNotUTF8IsRecordedAsReplacementCharacters(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("printf 'a\\377b\\n'\nprintf 'c\\376\\n' >&2\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || got != "run:x1\na\uFFFDb\n\n[stderr]\nc\uFFFD\n" {
		t.Fatalf("answer %q %v", got, err)
	}
	runs, _ := l.recorded()
	if len(runs) != 1 || runs[0]["output"] != "a\uFFFDb\n" || runs[0]["error"] != "c\uFFFD\n" || runs[0]["exit_status"] != float64(0) {
		t.Fatalf("posted %+v", runs)
	}
}

func TestRunScriptRefusesAPathOutsideTheRunFolder(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	outside := filepath.Join(t.TempDir(), "x.py")
	must(t, os.WriteFile(outside, []byte("echo out\n"), 0o644))
	must(t, os.Symlink(outside, filepath.Join(dir, "link.py")))
	// a relative path to a file that exists outside the folder
	up, err := filepath.Rel(dir, outside)
	must(t, err)
	for _, p := range []string{"../x.py", up, "/etc/x.py", outside, "link.py"} {
		if got, err := useTool(t, d, "run_script", map[string]any{"path": p}); err == nil {
			t.Errorf("%s ran: %q", p, got)
		}
	}
	if _, sizes := l.recorded(); len(sizes) != 0 {
		t.Fatalf("%d runs posted", len(sizes))
	}
}

func TestRunScriptRefusesAnUnknownExtension(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.sh"), []byte("echo hi > ran\n"), 0o644))
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.sh"}); err == nil {
		t.Fatalf("a.sh ran: %q", got)
	}
	if _, sizes := l.recorded(); len(sizes) != 0 {
		t.Fatalf("%d runs posted", len(sizes))
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Fatal("the script ran")
	}
}

// groupGone says whether the process group the script wrote to pgid has no
// process left.
func groupGone(t *testing.T, dir string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "pgid"))
	must(t, err)
	pgid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	must(t, err)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestAScriptPastItsTimeLimitIsStopped(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	d.ScriptTimeout = 300 * time.Millisecond
	// the background sleep is in the script's group, so killing the group stops it too
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo $$ > pgid\nsleep 10 &\nsleep 10\n"), 0o644))
	start := time.Now()
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || time.Since(start) > 5*time.Second {
		t.Fatalf("%q %v after %v", got, err, time.Since(start))
	}
	runs, _ := l.recorded()
	b := runs[0]
	if b["exit_status"] != float64(-1) || !strings.Contains(b["error"].(string), "the script ran past its time limit of 300ms and was stopped") {
		t.Fatalf("exit %v error %q", b["exit_status"], b["error"])
	}
	if !strings.Contains(got, "ran past its time limit") {
		t.Fatalf("answer %q", got)
	}
	if !groupGone(t, dir) {
		t.Fatal("the script's process group is still running")
	}
}

func TestATimeLimitInHoursReadsInHours(t *testing.T) {
	if got := limitText(2 * time.Hour); got != "2 hours" {
		t.Fatalf("%q", got)
	}
}

func TestClosingTheDoorStopsARunningScript(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo $$ > pgid\nsleep 10 &\nsleep 10\n"), 0o644))
	done := make(chan struct{})
	go func() { useTool(t, d, "run_script", map[string]any{"path": "a.py"}); close(done) }()
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(dir, "pgid")); return err == nil })
	start := time.Now()
	d.Close()
	if time.Since(start) > 5*time.Second {
		t.Fatalf("closing took %v", time.Since(start))
	}
	if !groupGone(t, dir) {
		t.Fatal("the script's process group is still running")
	}
	<-done
	if _, sizes := l.recorded(); len(sizes) != 0 {
		t.Fatalf("a script stopped by the run's end was recorded: %d posts", len(sizes))
	}
}

func TestAScriptKilledByASignalSaysSo(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("kill -TERM $$\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := l.recorded()
	if runs[0]["exit_status"] != float64(-1) || !strings.Contains(runs[0]["error"].(string), "the script was stopped by a signal: terminated") || !strings.Contains(got, "stopped by a signal") {
		t.Fatalf("exit %v error %q answer %q", runs[0]["exit_status"], runs[0]["error"], got)
	}
}

func TestARunWithoutACardHasNoKey(t *testing.T) {
	l := newLykeion(t)
	l.mu.Lock()
	l.noCard = true
	l.mu.Unlock()
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo 42\n"), 0o644))
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil || got != "42\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRunScriptCutsOutputAtTheCap(t *testing.T) {
	defer func(m int) { maxAnswer = m }(maxAnswer)
	maxAnswer = 100
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("head -c 300 /dev/zero | tr '\\0' a\n"), 0o644))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	if runs, _ := l.recorded(); runs[0]["output"].(string) != strings.Repeat("a", 100)+"\n[output cut at 32 MiB]\n" {
		t.Fatalf("output %q", runs[0]["output"])
	}
}

func TestARunBodyStaysUnderTheServersLimit(t *testing.T) {
	defer func(m int) { maxRunBody = m }(maxRunBody)
	maxRunBody = 4096
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("head -c 3000 /dev/zero | tr '\\0' a\nhead -c 3000 /dev/zero | tr '\\0' b >&2\n"), 0o644))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	runs, sizes := l.recorded()
	if sizes[0] > 4096 {
		t.Fatalf("a %d-byte body", sizes[0])
	}
	// the error is cut first
	b := runs[0]
	if b["output"] != strings.Repeat("a", 3000) || !strings.HasSuffix(b["error"].(string), runCut) {
		t.Fatalf("output %d bytes, error %q", len(b["output"].(string)), b["error"])
	}
}

func TestRunCodeKeepsASessionAndRecordsEachCell(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	for i, code := range []string{"x = 1", "print(x)"} {
		got, err := useTool(t, d, "run_code", map[string]any{"language": "python", "code": code})
		if want := fmt.Sprintf("run:x%d\n%d ran %s", i+1, i+1, code); err != nil || got != want {
			t.Fatalf("cell %d: %q %v, want %q", i+1, got, err, want)
		}
	}
	runs, _ := l.recorded()
	if len(runs) != 2 {
		t.Fatalf("%d runs posted", len(runs))
	}
	for i, code := range []string{"x = 1", "print(x)"} {
		b := runs[i]
		if b["tool"] != "run_code" || b["language"] != "python" || b["code"] != code || b["exit_status"] != float64(0) || b["packages"] != fakePackages {
			t.Fatalf("run %d: %v", i+1, b)
		}
	}
	got, err := useTool(t, d, "run_code", map[string]any{"language": "python", "code": "fail"})
	runs, _ = l.recorded()
	if err != nil || got != "run:x3\n\n[stderr]\nboom" || runs[2]["exit_status"] != float64(1) {
		t.Fatalf("a failing cell: %q %v %v", got, err, runs[2]["exit_status"])
	}
}

func TestTheAgentRestartsItsOwnSession(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	for _, step := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"language": "python", "code": "x = 1"}, "run:x1\n1 ran x = 1"},
		{map[string]any{"language": "python", "code": "", "restart": true}, "the python session restarted; its variables are gone"},
		{map[string]any{"language": "python", "code": "print(x)"}, "run:x2\n1 ran print(x)"},
		{map[string]any{"language": "python", "code": "y = 2", "restart": true}, "run:x3\n1 ran y = 2"},
	} {
		if got, err := useTool(t, d, "run_code", step.args); err != nil || got != step.want {
			t.Fatalf("%v: %q %v, want %q", step.args, got, err, step.want)
		}
	}
	if runs, _ := l.recorded(); len(runs) != 3 || runs[2]["code"] != "y = 2" {
		t.Fatalf("runs %v", runs)
	}
}

func TestACellThatInstallsAPackageSaysHowToRestart(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	for code, note := range map[string]bool{
		"%pip install scanpy": true, "!pip install x": true, "import subprocess; subprocess.run(['uv', 'pip', 'install', 'x'])": true,
		"subprocess.run([sys.executable, \"-m\", \"pip\", \"install\", \"humanize\"])": true, "pip3  install x": true,
		"uv PIP install x": true, "install.packages('x')": true, "BiocManager::install('x')": true, "Pkg.add(\"x\")": true,
		"print('installed')": false,
	} {
		got, err := useTool(t, d, "run_code", map[string]any{"language": "python", "code": code})
		if err != nil || strings.HasSuffix(got, "\n"+installNote) != note {
			t.Errorf("%q: %q %v, want the note %v", code, got, err, note)
		}
	}
	if runs, _ := l.recorded(); strings.Contains(fmt.Sprint(runs), "restart the session") {
		t.Error("the note reached the run record")
	}
}

func TestRunCodeAnswersNotReadyWhenTheBaseIsMissing(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	_, err := useTool(t, d, "run_code", map[string]any{"language": "r", "code": "1"})
	if err == nil || !strings.Contains(err.Error(), "the R environment isn't ready on this machine") {
		t.Fatalf("%v", err)
	}
	d.mu.Lock()
	started := d.kernels != nil
	d.mu.Unlock()
	if _, sizes := l.recorded(); len(sizes) != 0 || started {
		t.Fatalf("%d posts; sessions started %v", len(sizes), started)
	}
}

func TestARunIsWithheldWhenRecordingFails(t *testing.T) {
	l := newLykeion(t)
	l.mu.Lock()
	l.failRuns = true
	l.mu.Unlock()
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo secret-result\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err == nil || strings.Contains(got+err.Error(), "secret-result") {
		t.Fatalf("%q %v", got, err)
	}
	if _, sizes := l.recorded(); len(sizes) != 1 {
		t.Fatalf("%d posts", len(sizes))
	}
}

func TestTheCallTokenNeverReachesAScript(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("env\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "VIRTUAL_ENV=") || !strings.Contains(got, "PATH="+d.Study.PathPrefix()+string(os.PathListSeparator)) {
		t.Fatalf("the script did not run in the study's environment: %s", got)
	}
	for _, s := range []string{d.CallToken, d.RunToken, "LYK_TOKEN=", "LYK_DOOR_TOKEN=", "secret"} {
		if strings.Contains(got, s) {
			t.Fatalf("the script saw %q", s)
		}
	}
}

func TestARunsPathKeepsOneStudyPrefix(t *testing.T) {
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	d.Study.Env() // as a run does, before it reads the prefix
	sep, prefix := string(os.PathListSeparator), d.Study.PathPrefix()
	dir := t.TempDir()
	d.Child = func() agent.ExecOptions {
		return agent.ExecOptions{Dir: dir, Env: []string{"PATH=/self" + sep + prefix + sep + "/usr/bin"}}
	}
	pathOf := func(env []string) (path string) {
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, "PATH="); ok {
				path = v
			}
		}
		return path
	}
	// The CLI's own PATH may lead with folders of the run's.
	child := pathOf(agent.ChildEnv(d.Child()))
	env, err := d.runEnv(context.Background())
	must(t, err)
	if path := pathOf(env); path != child || !strings.HasSuffix(path, "/self"+sep+prefix+sep+"/usr/bin") {
		t.Fatalf("PATH %q", path)
	}
}

// Scripts, the session bridge and the study's package list each start
// through the sandbox, under one policy built when the door starts.
func TestCodeRunsInTheSandbox(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	s := &sandboxed{}
	d := newRunDoor(t, l, dir, func(d *Door) { d.wrap = stubSandbox(s) }, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo 42\n"), 0o644))
	// a cell first, so the sandbox is built for the session bridge; the
	// Python interpreter is the stand-in bridge until the script runs
	t.Setenv("LYK_KERNEL_STUB", "1")
	if got, err := useTool(t, d, "run_code", map[string]any{"language": "python", "code": "x = 1"}); err != nil || got != "run:x1\n1 ran x = 1" {
		t.Fatalf("%q %v", got, err)
	}
	t.Setenv("LYK_KERNEL_STUB", "0")
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil || got != "run:x2\n42\n" {
		t.Fatalf("%q %v", got, err)
	}
	if runs, _ := l.recorded(); len(runs) != 2 || runs[0]["packages"] != fakePackages || runs[1]["packages"] != fakePackages {
		t.Fatalf("runs %v", runs)
	}
	commands, policies := s.seen()
	venv := filepath.Join(d.Study.Dir, "python", "bin", "python")
	has := func(want ...string) bool {
		return slices.ContainsFunc(commands, func(c []string) bool {
			return len(c) == len(want) && slices.Equal(c[:len(want)-1], want[:len(want)-1]) && strings.HasPrefix(c[len(want)-1], want[len(want)-1])
		})
	}
	script, err := filepath.EvalSymlinks(filepath.Join(dir, "a.py"))
	must(t, err)
	if !has("/bin/sh", script) {
		t.Errorf("the script did not start in the sandbox: %q", commands)
	}
	if !has(filepath.Join(d.Study.Bases.Dir, "python", "bin", "python"), "-m", "venv", "--system-site-packages", venv[:len(venv)-len("/bin/python")]) {
		t.Errorf("the study's venv was not made in the sandbox: %q", commands)
	}
	if !has(os.Args[0], "-I", filepath.Join(d.bridgeDir, "lyk-bridge-")) {
		t.Errorf("the session bridge did not start in the sandbox from the door's bridge folder: %q", commands)
	}
	if !has(venv, "-m", "pip", "freeze", "--local") {
		t.Errorf("pip freeze, which loads the study's own packages, did not run in the sandbox: %q", commands)
	}
	u, err := user.Current()
	must(t, err)
	want := sandbox.Policy{Write: []string{dir, d.Study.Dir, d.tmp}, Read: []string{d.bridgeDir, filepath.Join(d.Study.Bases.Dir, "python")}, Home: u.HomeDir}
	for _, p := range policies {
		if !slices.Equal(p.Write, want.Write) || !slices.Equal(p.Read, want.Read) || p.Home != want.Home {
			t.Fatalf("policy %+v, want %+v", p, want)
		}
	}
	// the study folder is every run's of the study, the temp folder this
	// run's alone
	for _, shared := range []string{d.Study.Dir, dir} {
		if rel, err := filepath.Rel(shared, d.tmp); err == nil && filepath.IsLocal(rel) {
			t.Fatalf("temp folder %q is in %q", d.tmp, shared)
		}
	}
}

// With no sandbox, code tools answer why, start nothing and record nothing.
func TestCodeRunsNowhereWithoutTheSandbox(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	for name, setup := range map[string]func(*Door){
		"failed its check": func(d *Door) { d.SandboxErr = errors.New("install bubblewrap") },
		"refused the policy": func(d *Door) {
			d.wrap = func(context.Context, sandbox.Policy, string, ...string) (*exec.Cmd, error) {
				return nil, errors.New("install bubblewrap")
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLykeion(t)
			dir := t.TempDir()
			d := newRunDoor(t, l, dir, setup, envs.Python)
			must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo hi > ran\n"), 0o644))
			const want = "analysis code can't run on this machine: the sandbox isn't available (install bubblewrap)"
			for _, call := range []struct {
				tool string
				args map[string]any
			}{{"run_script", map[string]any{"path": "a.py"}}, {"run_code", map[string]any{"language": "python", "code": "x = 1"}}} {
				if got, err := useTool(t, d, call.tool, call.args); err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("%s: %q %v", call.tool, got, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
				t.Error("the script ran")
			}
			if _, err := os.Stat(filepath.Join(d.Study.Dir, "python", "bin", "python.ran")); err == nil {
				t.Error("the study's interpreter ran")
			}
			d.mu.Lock()
			started := d.kernels != nil
			d.mu.Unlock()
			if _, sizes := l.recorded(); len(sizes) != 0 || started {
				t.Fatalf("%d posts; sessions started %v", len(sizes), started)
			}
		})
	}
}

// Caches that default to the home folder, which the sandbox hides, are kept
// in the run's temp folder; none of the run's tokens reach the code.
func TestCodeKeepsItsCachesInTheRunsTempFolder(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("env\n"), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(got, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	if env["TMPDIR"] != d.tmp {
		t.Errorf("TMPDIR %q, want %q", env["TMPDIR"], d.tmp)
	}
	for _, k := range []string{"PIP_CACHE_DIR", "JUPYTER_RUNTIME_DIR", "JUPYTER_DATA_DIR", "IPYTHONDIR", "MPLCONFIGDIR", "XDG_CACHE_HOME", "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		if rel, err := filepath.Rel(d.tmp, env[k]); env[k] == "" || err != nil || !filepath.IsLocal(rel) {
			t.Errorf("%s=%q is not in the run's temp folder %q", k, env[k], d.tmp)
		}
	}
	for _, k := range []string{"LYK_TOKEN", "LYK_DOOR_TOKEN", "LYK_DOOR", "LYK_SERVER", "LYK_CONFIG"} {
		if _, ok := env[k]; ok {
			t.Errorf("the script saw %s", k)
		}
	}
	for _, s := range []string{d.CallToken, d.RunToken, d.Token, "secret"} {
		if strings.Contains(got, s) {
			t.Errorf("the script saw %q", s)
		}
	}
}

func TestClosingTheDoorRemovesItsTempFolder(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo hi\n"), 0o644))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(d.tmp); err != nil || !fi.IsDir() {
		t.Fatalf("no temp folder: %v", err)
	}
	d.Close()
	if _, err := os.Stat(d.tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the temp folder is still there: %v", err)
	}
}

// The run's code may leave anything at a script's path; the door reads only
// a regular file and never waits on one that isn't.
func TestRunScriptReadsOnlyARegularFile(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, syscall.Mkfifo(filepath.Join(dir, "a.py"), 0o644))
	start := time.Now()
	if got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err == nil {
		t.Fatalf("a pipe ran: %q", got)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("reading a pipe took %v", time.Since(start))
	}
	if _, sizes := l.recorded(); len(sizes) != 0 {
		t.Fatalf("%d runs posted", len(sizes))
	}
}

// The CLI's own files in the run folder are read-only to code: those that
// exist, and placeholders for the CLI's config that doesn't yet, which go
// when the door closes. A marker that doesn't exist is never made.
func TestTheCLIsOwnFilesAreReadOnlyToCode(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0o700))
	must(t, os.MkdirAll(filepath.Join(dir, ".cli-home"), 0o700))
	must(t, os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(`{"a":1}`), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{"x":1}`), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, ".lyk-mcp.json"), []byte(`{"token":"t"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, ".agent-lock"), nil, 0o600))
	existed := []string{".claude", ".cli-home", ".mcp.json", ".lyk-mcp.json", ".agent-lock"}
	s := &sandboxed{}
	d := newRunDoor(t, l, dir, func(d *Door) { d.wrap = stubSandbox(s) }, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo hi\n"), 0o644))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	_, policies := s.seen()
	if len(policies) == 0 {
		t.Fatal("nothing ran")
	}
	ro, hidden := policies[len(policies)-1].ReadOnly, policies[len(policies)-1].Hidden
	for _, p := range agent.RunFolderConfig {
		path := filepath.Join(dir, p.Name)
		fi, err := os.Lstat(path)
		had := slices.Contains(existed, p.Name)
		switch {
		case strings.Contains(p.Name, "/") && !had:
			if err == nil || slices.Contains(ro, path) || slices.Contains(hidden, path) {
				t.Errorf("%s: made or carved out", p.Name)
			}
		case p.Hidden:
			if !slices.Contains(hidden, path) || slices.Contains(ro, path) || err != nil {
				t.Errorf("%s: not hidden: %v", p.Name, err)
			}
		case p.Marker && !had:
			if err == nil || slices.Contains(ro, path) || slices.Contains(hidden, path) {
				t.Errorf("%s: made or carved out", p.Name)
			}
		default:
			if !slices.Contains(ro, path) || err != nil || fi.IsDir() != p.Folder {
				t.Errorf("%s: read-only %v, on disk %v %v", p.Name, slices.Contains(ro, path), fi, err)
			}
		}
	}
	for name, want := range map[string]string{".lyk-config.json": "{}", ".claude.json": "{}", "CLAUDE.md": "", ".agent-exit.tmp": ""} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("placeholder %s: %q %v", name, b, err)
		}
	}
	d.Close()
	for _, name := range []string{".codex", ".git", ".lyk-config.json", "CLAUDE.md", ".claude.json"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			t.Errorf("placeholder %s left behind", name)
		}
	}
	for name, want := range map[string]string{".claude/settings.json": `{"a":1}`, ".mcp.json": `{"x":1}`, ".lyk-mcp.json": `{"token":"t"}`} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
}

// The script runs from its own path, so paths relative to it work. If it
// changed while it ran, the record keeps what was read and says so.
func TestAScriptThatChangesWhileItRunsIsNoted(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	resolved, err := filepath.EvalSymlinks(dir)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo \"$0\"\n"), 0o644))
	rewrites := "echo 'echo changed' > \"$0\"\necho ran\n"
	must(t, os.WriteFile(filepath.Join(dir, "b.py"), []byte(rewrites), 0o644))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil || got != "run:x1\n"+filepath.Join(resolved, "a.py")+"\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "b.py"}); err != nil {
		t.Fatal(err)
	}
	runs, _ := l.recorded()
	const note = "the script changed while it ran; the recorded script may not be what ran"
	if strings.Contains(runs[0]["error"].(string), note) {
		t.Errorf("a script that didn't change was noted: %q", runs[0]["error"])
	}
	if !strings.Contains(runs[1]["error"].(string), note) || runs[1]["script"] != rewrites || runs[1]["output"] != "ran\n" {
		t.Fatalf("record %v", runs[1])
	}
}

// A run may move to a fresh folder before its CLI starts, so the sandbox is
// built for the folder the first code runs in.
func TestTheSandboxIsBuiltForTheRunsFinalFolder(t *testing.T) {
	l := newLykeion(t)
	first, final := t.TempDir(), t.TempDir()
	s := &sandboxed{}
	d := newRunDoor(t, l, first, func(d *Door) { d.wrap = stubSandbox(s) }, envs.Python)
	d.Child = func() agent.ExecOptions { return agent.ExecOptions{Dir: final} }
	must(t, os.WriteFile(filepath.Join(final, "a.py"), []byte("echo hi > out\n"), 0o644))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	_, policies := s.seen()
	if len(policies) == 0 || policies[0].Write[0] != final {
		t.Fatalf("policies %+v", policies)
	}
	if es, _ := os.ReadDir(first); len(es) != 0 {
		t.Fatalf("the first folder was touched: %v", es)
	}
}

// Routing a skill never writes or removes through a link the run's code
// planted in the skills folder's path.
func TestARoutedSkillIsNotWrittenThroughALink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	must(t, os.MkdirAll(filepath.Join(outside, "lyk-x"), 0o700))
	must(t, os.WriteFile(filepath.Join(outside, "lyk-x", "keep"), []byte("mine"), 0o600))
	must(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0o700))
	must(t, os.Symlink(outside, filepath.Join(dir, ".claude", "skills")))
	if err := placeRouted("claude", dir, toolroute.RoutedSkill{Name: "x"}); err == nil {
		t.Fatal("placed through a link")
	}
	if b, err := os.ReadFile(filepath.Join(outside, "lyk-x", "keep")); err != nil || string(b) != "mine" {
		t.Fatalf("the folder the link leads to changed: %q %v", b, err)
	}
}

// The bridge's file is written where code can't put a link: not in the
// run's temp folder, which code may write, but in a folder of the door's
// that code may only read.
func TestTheBridgeIsNotWrittenThroughALink(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	s := &sandboxed{}
	d := newRunDoor(t, l, dir, func(d *Door) { d.wrap = stubSandbox(s) }, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo hi\n"), 0o644))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	must(t, os.RemoveAll(d.tmp))
	must(t, os.Symlink(outside, d.tmp))
	t.Setenv("LYK_KERNEL_STUB", "1")
	if _, err := useTool(t, d, "run_code", map[string]any{"language": "python", "code": "x = 1"}); err != nil {
		t.Fatal(err)
	}
	if es, _ := os.ReadDir(outside); len(es) != 0 {
		t.Fatalf("written through the link: %v", es)
	}
	commands, policies := s.seen()
	i := slices.IndexFunc(commands, func(c []string) bool { return c[0] == os.Args[0] })
	if i < 0 {
		t.Fatalf("no bridge started: %q", commands)
	}
	bridge := commands[i]
	if len(bridge) != 3 || !slices.ContainsFunc(policies[len(policies)-1].Read, func(r string) bool { return strings.HasPrefix(bridge[2], r+string(os.PathSeparator)) }) {
		t.Fatalf("the bridge %q is not in a read-only folder of %+v", bridge, policies[len(policies)-1])
	}
}

// The door's runner supplies the run's environment around what the study
// adds: the daemon's own settings and the person's Python setup never reach
// a study step.
func TestTheRunnerUsesTheRunsEnvironment(t *testing.T) {
	t.Setenv("LYK_DAEMON_SETTING", "daemon-setting-sentinel")
	t.Setenv("lyk_daemon_setting", "lowercase-setting-sentinel")
	t.Setenv("CLAUDE_CONFIG_DIR", "/the/daemon/config")
	t.Setenv("PYTHONHOME", "/the/persons/python")
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	out, err := d.Study.Run(context.Background(), []string{"STUDY=1"}, dir, "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	env := "\n" + string(out)
	for _, bad := range []string{"/the/daemon/config", "/the/persons/python", "LYK_", "daemon-setting-sentinel", "lowercase-setting-sentinel"} {
		if strings.Contains(env, bad) {
			t.Errorf("%q reached the step", bad)
		}
	}
	for _, want := range []string{"\nSTUDY=1\n", "\nPATH=", "\nTMPDIR=" + d.tmp + "\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("no %q in %q", want, env)
		}
	}
}

// The person's credentials and their own Python, R, Julia and conda settings
// never reach the run's code, by name; what the network and the locale need
// does, and so does the study's own environment.
func TestCodeGetsNoSecretsOrThePersonsSettings(t *testing.T) {
	planted := map[string]string{"GITHUB_TOKEN": "t1", "OPENAI_API_KEY": "t2", "My_Secret": "t3", "DB_PASSWORD": "t4", "PGPASSWD": "t5",
		"GIT_CREDENTIAL_HELPER": "t6", "AWS_REGION": "t7", "GOOGLE_APPLICATION_CREDENTIALS": "t8", "AZURE_TENANT_ID": "t9",
		"PYTHONPATH": "/persons/py", "PYTHONHOME": "/persons/home", "CONDA_PREFIX": "/persons/conda", "MAMBA_ROOT_PREFIX": "/persons/mamba",
		"R_LIBS_USER": "/persons/r", "R_PROFILE_USER": "/persons/rprofile", "JULIA_DEPOT_PATH": "/persons/julia", "VIRTUAL_ENV": "/persons/venv"}
	kept := map[string]string{"HTTPS_PROXY": "http://proxy:3128", "NO_PROXY": "localhost", "SSL_CERT_FILE": "/etc/ssl/cert.pem",
		"REQUESTS_CA_BUNDLE": "/etc/ssl/cert.pem", "LANG": "en_US.UTF-8"}
	for k, v := range planted {
		t.Setenv(k, v)
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("env\n"), 0o644))
	script, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err != nil {
		t.Fatal(err)
	}
	step, err := d.Study.Run(context.Background(), nil, dir, "/usr/bin/env")
	must(t, err)
	for name, got := range map[string]string{"script": script, "step": string(step)} {
		lines := strings.Split(got, "\n")
		for k, v := range planted {
			if slices.Contains(lines, k+"="+v) {
				t.Errorf("the %s saw %s=%s", name, k, v)
			}
		}
		for k, v := range kept {
			if !slices.Contains(lines, k+"="+v) {
				t.Errorf("the %s lost %s", name, k)
			}
		}
	}
	if !strings.Contains(script, "\nVIRTUAL_ENV="+d.Study.Dir) {
		t.Errorf("the script lost the study's own VIRTUAL_ENV: %s", script)
	}
}

func TestRunCodeSeesStudyVariables(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	d.StudyEnv = map[string]string{"PYTHONPATH": "sdk", "X": "user", "VIRTUAL_ENV": "bad", "PATH": "/bad", "LYK_DOOR": "bad", "HOME": "bad", "CODEX_HOME": "bad", "TMPDIR": "bad"}
	child := d.Child
	d.Child = func() agent.ExecOptions {
		opts := child()
		opts.Env = append(opts.Env, "PATH="+os.Getenv("PATH"), "LYK_DOOR=generated", "X=generated")
		opts.Env = mergeEnv(opts.Env, d.StudyEnv)
		return opts
	}
	got, err := useTool(t, d, "run_code", map[string]any{"language": "python", "code": "env:PYTHONPATH"})
	must(t, err)
	if !strings.HasSuffix(got, "sdk") {
		t.Fatalf("kernel env: %q", got)
	}
	got, err = useTool(t, d, "run_code", map[string]any{"language": "python", "code": "env:X"})
	must(t, err)
	if !strings.HasSuffix(got, "generated") {
		t.Fatalf("generated kernel env was replaced: %q", got)
	}
	env, err := d.runEnv(context.Background())
	must(t, err)
	for _, e := range []string{"PATH=/bad", "VIRTUAL_ENV=bad", "HOME=bad", "CODEX_HOME=bad", "TMPDIR=bad", "LYK_DOOR=bad"} {
		if slices.Contains(env, e) {
			t.Fatalf("study replaced generated or private env: %s", strings.SplitN(e, "=", 2)[0])
		}
	}
	must(t, os.WriteFile(filepath.Join(dir, "env.py"), []byte(`printf '%s' "$PYTHONPATH"`), 0600))
	t.Setenv("LYK_KERNEL_STUB", "0")
	got, err = useTool(t, d, "run_script", map[string]any{"path": "env.py"})
	must(t, err)
	if !strings.HasSuffix(got, "sdk") {
		t.Fatalf("script env: %q", got)
	}
}
