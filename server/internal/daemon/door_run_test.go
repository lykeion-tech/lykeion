package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/daemon/envs"
	"lykeion/server/pkg/agent"
)

// stubBridge stands in for the session bridge: it answers each exec with the
// session's cell count and the code, so two cells in one session read 1 and
// 2, and "fail" answers as a cell that raised.
func stubBridge() {
	n := 0
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID   int    `json:"id"`
			Op   string `json:"op"`
			Code string `json:"code"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.Op == "shutdown" {
			return
		}
		n++
		r := map[string]any{"id": req.ID, "stdout": fmt.Sprintf("%d ran %s", n, req.Code), "ok": true}
		if req.Code == "fail" {
			r = map[string]any{"id": req.ID, "error": "boom", "ok": false}
		}
		b, _ := json.Marshal(r)
		os.Stdout.Write(append(b, '\n'))
	}
}

// fakeStudy is a study over bases marked ready without building them: pip
// freeze answers one add-on package and venv makes an empty interpreter.
func fakeStudy(t *testing.T, langs ...string) *envs.Study {
	t.Helper()
	run := func(_ context.Context, _ []string, _, name string, args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case len(args) == 4 && args[1] == "venv":
			must(t, os.MkdirAll(filepath.Join(args[3], "bin"), 0o755))
			return nil, os.WriteFile(filepath.Join(args[3], "bin", "python"), nil, 0o755)
		case strings.Contains(line, "freeze --local"):
			return []byte("requests==2.32.0\n"), nil
		}
		return nil, nil
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
	return envs.ForStudy(b, t.TempDir())
}

const fakePackages = "# python base\npython-lock\n\n# python study\nrequests==2.32.0\n"

// runDoor is a door over a study with a ready Python base whose scripts run
// under /bin/sh and whose session bridge is the test binary.
func runDoor(t *testing.T, l *lykeion, dir string, langs ...string) *Door {
	t.Helper()
	d := openDoor(t, l, dir, nil)
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
	return d
}

// useTool calls one of the door's tools as the CLI would and answers its text.
func useTool(t *testing.T, d *Door, name string, args map[string]any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	d.Child = func() agent.ExecOptions {
		return agent.ExecOptions{Dir: t.TempDir(), Env: []string{"PATH=/self" + sep + prefix + sep + "/usr/bin"}}
	}
	var path string
	for _, e := range d.runEnv() {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	if path != "/self"+sep+prefix+sep+"/usr/bin" {
		t.Fatalf("PATH %q", path)
	}
}
