package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/accountlookup"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/kernels"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/pkg/agent"
)

const (
	runCodeDescription = "Run code in a live session (language: python, r or julia) that keeps its variables between calls, in this study's environment. " +
		"For exploring; a reported result must come from run_script. " +
		"Pass restart: true to restart the session first, clearing its variables; with no code, only the restart happens."
	runScriptDescription = "Run a saved script (.py, .R or .jl, a path inside your working folder) from start to finish in a fresh process, in this study's environment."
	runScriptAttestation = " List JSON result paths in outputs to bind values to this execution."
	// runCodeCite and runScriptCite tell a run that cites by hand how.
	runCodeCite   = " The answer starts with run:<key>, which you may cite as run:<key>."
	runScriptCite = " Cite the result's numbers as run:<key>, the first line of the answer."
	// maxScript is the longest script or cell the server records.
	maxScript = 256 << 10
	// maxSetupOutput matches the existing per-call output limit.
	maxSetupOutput = 32 << 20
	// outputCut ends output dropped past maxAnswer, as the session bridge
	// ends a cell's.
	outputCut     = "\n[output cut at 32 MiB]\n"
	scriptChanged = "the script changed while it ran; the recorded script may not be what ran"
	// runCut ends output dropped so the record fits the server's body limit.
	runCut = "\n[output cut to fit the run record]\n"
)

// maxRunBody is the largest body the server takes on /api/role/runs.
var maxRunBody = middleware.MaxCallBodyBytes

var scriptLanguages = map[string]string{".py": envs.Python, ".R": envs.R, ".r": envs.R, ".jl": envs.Julia}

type runCodeIn struct {
	Language string `json:"language" jsonschema:"python, r or julia"`
	Code     string `json:"code" jsonschema:"the code to run in the language's session"`
	Restart  bool   `json:"restart,omitempty" jsonschema:"restart the language's session before running code; its variables are gone"`
}

// installNote ends the answer to a cell that installs packages, which a
// running session may not load until it restarts.
const installNote = "If the new package doesn't load, restart the session with run_code(language, code, restart: true) and re-run what you need."

// installs finds a package install in a cell, written as a command or as a
// list of arguments such as ["pip", "install", ...].
var installs = regexp.MustCompile(`(?i)pip3?\s+install|%pip|!pip|install\.packages\(|BiocManager::install\(|Pkg\.add\(|pip3?['"]\s*,\s*['"]install`)

type runScriptLegacyIn struct {
	Path string `json:"path" jsonschema:"the script's path, relative to your working folder"`
}

type runScriptIn struct {
	Path    string   `json:"path" jsonschema:"the script's path, relative to your working folder"`
	Outputs []string `json:"outputs,omitempty" jsonschema:"up to 16 distinct JSON result paths under outputs/; only files created or changed by a successful run are verified"`
}

func (d *Door) interpreter(lang string) (string, []string, error) {
	var name string
	var args []string
	var err error
	if d.interp != nil {
		name, args, err = d.interp(lang)
	} else if d.Study == nil {
		return "", nil, errors.New("this run has no study environment")
	} else {
		name, args, err = d.Study.Interpreter(lang)
	}
	d.recordEnvironmentError(lang, err)
	return name, args, err
}

func (d *Door) recordEnvironmentError(lang string, err error) {
	if !errors.Is(err, envs.ErrNotReady) {
		return
	}
	var named interface{ Language() string }
	if errors.As(err, &named) {
		lang = named.Language()
	}
	d.setEnvironmentFailure(lang, true)
}

func (d *Door) setEnvironmentFailure(lang string, failed bool) {
	if lang != envs.Python && lang != envs.R && lang != envs.Julia {
		return
	}
	d.environmentMu.Lock()
	defer d.environmentMu.Unlock()
	if failed {
		if d.environmentFailures == nil {
			d.environmentFailures = make(map[string]bool)
		}
		d.environmentFailures[lang] = true
	} else {
		delete(d.environmentFailures, lang)
	}
}

func (d *Door) clearReadyEnvironment(lang string) {
	d.setEnvironmentFailure(lang, false)
	if lang != envs.Python && d.Study != nil && d.Study.Ready(envs.Python) == nil {
		d.setEnvironmentFailure(envs.Python, false)
	}
}

// EnvironmentFailures reports unresolved daemon-observed environment failures.
func (d *Door) EnvironmentFailures() []string {
	d.environmentMu.Lock()
	defer d.environmentMu.Unlock()
	var result []string
	for _, lang := range []string{envs.Julia, envs.Python, envs.R} {
		if d.environmentFailures[lang] {
			result = append(result, lang)
		}
	}
	return result
}

// sandboxReady builds the run's sandbox once, before its first code runs:
// by then the run is in the folder it keeps, which may not be the one it
// started in. It answers why code can't run, if it can't.
func (d *Door) sandboxReady() error {
	d.sandboxMu.Lock()
	defer d.sandboxMu.Unlock()
	if d.sandboxErr == nil && !d.sandboxBuilt {
		d.sandboxBuilt = true
		d.sandboxErr = d.sandboxPolicy()
	}
	if d.sandboxErr != nil {
		return unsandboxed(d.sandboxErr)
	}
	return nil
}

// sandboxPolicy builds the run's sandbox: its code writes only the run
// folder, the study's add-on folder and the run's private temp folder, and
// reads those and the language bases. The temp folder lies outside the
// study's folder, which every run of the study shares, so another run's code
// can't read this one's caches or kernel connection files. The bases are listed one
// by one, since their parent folder holds every study's add-on folder. What
// the CLIs and the daemon keep in the run folder is read-only to the code.
func (d *Door) sandboxPolicy() error {
	if d.Study == nil {
		return errors.New("this run has no study environment")
	}
	if d.ctx.Err() != nil {
		return errors.New("the run has ended")
	}
	home := d.home
	if home == "" {
		u, err := accountlookup.Current()
		if err != nil {
			return err
		}
		home = u.HomeDir
	}
	tmp, err := os.MkdirTemp("", "lyk-run-")
	if err != nil {
		return err
	}
	d.tmp = tmp
	if err := os.Mkdir(filepath.Join(tmp, "home"), 0o700); err != nil {
		return err
	}
	dir := d.Child().Dir
	d.policy = codePolicy(home, dir, d.Study, tmp, d.PrivatePaths, nil)
	d.policy.StrictRead = d.Guest
	// Scripts never need the CLIs' homes, which hold the person's sign-ins
	// and config, wherever those homes live.
	d.policy.Hidden = hideCLIHomes(d.policy.Hidden, home)
	for _, root := range append(slices.Clone(d.PrivateRoots), d.Study.Bases.Dir) {
		if root == "" || slices.Contains(d.policy.RestrictedRoots, root) {
			continue
		}
		if err := sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return err
		}
		d.policy.RestrictedRoots = append(d.policy.RestrictedRoots, root)
	}

	if err := protectRunFolder(&d.policy, dir, true, &d.placeholders); err != nil {
		return err
	}
	// The session bridge's file goes in a folder of the door's own, outside
	// every folder the code may write, so no link the code makes is followed
	// when it is written. The code may only read it.
	if d.bridgeDir, err = os.MkdirTemp("", "lyk-bridge-"); err != nil {
		return err
	}
	d.policy.Read = append(d.policy.Read, d.bridgeDir)
	// Reserve the trusted root before the policy is fixed so pairs built
	// during this run are readable without exposing other studies.
	if err := d.Study.Bases.EnsureLeanRoot(); err != nil {
		return err
	}
	// Refresh the base grants after reserving Lean, preserving the door's
	// bridge-first read layout.
	d.policy.Read = append([]string{d.bridgeDir}, codePolicy(home, dir, d.Study, tmp, d.PrivatePaths, nil).Read...)
	return nil
}

// placeholder stands in, read-only to the code, for a CLI's config the run
// folder doesn't hold, so the code can't create it. A JSON one holds an
// empty object, which its readers take as no config.
type placeholder struct {
	path, content string
	folder        bool
}

func makePlaceholder(path string, folder bool) (placeholder, error) {
	p := placeholder{path: path, folder: folder}
	if folder {
		return p, os.Mkdir(path, 0o700)
	}
	if strings.HasSuffix(path, ".json") {
		p.content = "{}"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return p, err
	}
	_, err = f.WriteString(p.content)
	return p, errors.Join(err, f.Close())
}

// remove takes the placeholder away unless the CLI has since used it: a
// folder only while empty, a file only while it holds what was written.
func (p placeholder) remove() {
	if p.folder {
		os.Remove(p.path)
		return
	}
	fi, err := os.Lstat(p.path)
	if b, rerr := os.ReadFile(p.path); err == nil && rerr == nil && fi.Mode().IsRegular() && string(b) == p.content {
		os.Remove(p.path)
	}
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// command starts name with args in the run's sandbox. Code never runs
// outside it: with no working sandbox, nothing starts.
func (d *Door) command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	if err := d.sandboxReady(); err != nil {
		return nil, err
	}
	wrap := d.wrap
	if wrap == nil {
		wrap = func(ctx context.Context, p sandbox.Policy, name string, args ...string) (*exec.Cmd, error) {
			return p.Command(ctx, name, args...)
		}
	}
	cmd, err := wrap(ctx, d.policy, name, args...)
	if err != nil {
		return nil, unsandboxed(err)
	}
	return cmd, nil
}

// unsandboxedError is why code can't run: the sandbox isn't available.
type unsandboxedError struct{ err error }

func (e unsandboxedError) Error() string {
	return fmt.Sprintf("analysis code can't run on this machine: the sandbox isn't available (%v)", e.err)
}

func unsandboxed(err error) error { return unsandboxedError{err} }

// runSandboxed runs a step in the study's folders in the sandbox and
// answers its stdout. Its environment is the person's, as personEnv leaves
// it, then env, which holds what the study adds, then temp files and caches
// in the run's temp folder. It can't be the full environment code runs with,
// which comes from the study's folders this step may be making.
func (d *Door) runSandboxed(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := d.command(ctx, name, args...)
	if err != nil {
		return nil, err
	}
	defer sandbox.CloseCommand(cmd)
	env = slices.DeleteFunc(slices.Clone(env), func(e string) bool { return strings.HasPrefix(e, "LYK_") })
	cmd.Dir, cmd.Env = dir, slices.Concat(d.personEnv(), env, d.tmpEnv())
	stdout, stderr := setupOutput{cancel: cancel}, setupOutput{cancel: cancel}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("environment setup output exceeds 32 MiB")
	}
	out := stdout.buf.Bytes()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(stderr.buf.String()))
	}
	return out, nil
}

type setupOutput struct {
	buf      bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (o *setupOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxSetupOutput - o.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
		o.exceeded = true
		// shortcut: descendants may survive an exited leader; add an OS-owned supervisor.
		o.cancel()
	}
	_, _ = o.buf.Write(p)
	return n, nil
}

// cacheDirs are where the home folder and the caches and config that
// default to it go in the run's temp folder: the person's home is hidden
// and the CLI's own is read-only to code.
var cacheDirs = map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "PIP_CACHE_DIR": "pip", "JUPYTER_RUNTIME_DIR": "jupyter-runtime", "JUPYTER_DATA_DIR": "jupyter-data",
	"IPYTHONDIR": "ipython", "MPLCONFIGDIR": "matplotlib", "XDG_CACHE_HOME": "cache"}

// tmpEnv points temp files and caches into the run's temp folder.
func (d *Door) tmpEnv() []string {
	if d.tmp == "" {
		return nil
	}
	env := []string{"TMPDIR=" + d.tmp}
	for k, sub := range cacheDirs {
		env = append(env, k+"="+filepath.Join(d.tmp, sub))
	}
	return env
}

// secretNames are what a variable's name holds when its value is likely a
// credential, matched in any case.
var secretNames = []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL"}

func privateCodeEnvName(k string) bool {
	k = strings.ToUpper(k)
	return strings.HasPrefix(k, "LYK_") || slices.Contains([]string{"HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "XDG_DATA_HOME", "XDG_DATA_DIRS", "XDG_CACHE_HOME", "XDG_STATE_HOME"}, k)
}

// personEnv is the person's environment as code gets it: the connectors'
// one, without daemon settings or what looks like a
// credential by its name (cloud accounts included) and without the person's
// own Python, R, Julia and conda settings. The proxy and certificate
// variables stay for the network; a proxy URL that carries a password
// reaches the code with it.
func (d *Door) personEnv() []string {
	opts := d.Child()
	if d.Guest {
		opts.Guest = true
	}
	return slices.DeleteFunc(envs.Isolate(connectorEnv(opts, nil)), func(e string) bool {
		k, _, _ := strings.Cut(e, "=")
		k = strings.ToUpper(k)
		return privateCodeEnvName(k) || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "AZURE_") ||
			slices.ContainsFunc(secretNames, func(s string) bool { return strings.Contains(k, s) })
	})
}

// runEnv is the environment code runs with: the person's, as personEnv
// leaves it, with the study's folders and interpreters on PATH ahead of the
// person's own, and temp files and caches in the run's temp folder.
func (d *Door) runEnv(ctx context.Context) ([]string, error) {
	study, err := d.Study.Environment(ctx)
	if err != nil {
		return nil, err
	}
	env := append(append(d.personEnv(), study...), d.tmpEnv()...)
	path := os.Getenv("PATH")
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	// The CLI's PATH holds none of the study's folders; they go first, once.
	sep := string(os.PathListSeparator)
	if prefix := d.Study.PathPrefix(); prefix != "" && !strings.Contains(sep+path+sep, sep+prefix+sep) {
		path = prefix + sep + path
	}
	return append(append(env, "PATH="+path), d.ServiceEnv...), nil
}

// runContext ends when the call's does or when the door closes.
func (d *Door) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (d *Door) runCode(ctx context.Context, _ *mcp.CallToolRequest, in runCodeIn) (*mcp.CallToolResult, any, error) {
	d.executionMu.Lock()
	defer d.executionMu.Unlock()
	if _, _, err := d.interpreter(in.Language); err != nil {
		return nil, nil, err
	}
	if len(in.Code) > maxScript {
		return nil, nil, errors.New("the code is over 256 KiB, more than a run record keeps")
	}
	ctx, cancel := d.runContext(ctx)
	defer cancel()
	s, err := d.liveSessions(ctx)
	if err != nil {
		return nil, nil, err
	}
	if in.Restart {
		if err := s.Restart(ctx, in.Language); err != nil {
			return nil, nil, err
		}
		d.clearReadyEnvironment(in.Language)
		if in.Code == "" {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "the " + in.Language + " session restarted; its variables are gone"}}}, nil, nil
		}
	}
	packages, err := d.Study.Packages(ctx, in.Language)
	if err != nil {
		d.recordEnvironmentError(in.Language, err)
		return nil, nil, fmt.Errorf("reading the study's package list: %w", err)
	}
	res, err := s.Exec(ctx, in.Language, in.Code)
	if err != nil {
		return nil, nil, err
	}
	d.clearReadyEnvironment(in.Language)
	if err := d.Study.CheckDefaultPython(ctx); err != nil {
		return nil, nil, err
	}
	// a cell has no exit status of its own: one that raised counts as 1
	status := 0
	if !res.OK {
		status = 1
	}
	out, _, err := d.answerRun(ctx, map[string]any{"tool": "run_code", "language": in.Language, "code": in.Code,
		"output": res.Stdout, "error": res.Error, "exit_status": status, "packages": packages})
	if err == nil && installs.MatchString(in.Code) {
		out.Content[0].(*mcp.TextContent).Text += "\n" + installNote
	}
	return out, nil, err
}

// liveSessions are the run's live sessions, started on the first cell with the
// Python base's interpreter, which runs the bridge for every language.
func (d *Door) liveSessions(ctx context.Context) (*kernels.Sessions, error) {
	if d.kernelGeneration != d.Study.PythonGeneration() {
		if err := d.refreshPythonSession(ctx); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	s, closed := d.kernels, d.closed
	d.mu.Unlock()
	if closed {
		return nil, errors.New("the run has ended")
	}
	if s != nil {
		return s, nil
	}
	// resolving may create the study's venv, so it runs outside the lock
	python, _, err := d.interpreter(envs.Python)
	if err != nil {
		return nil, err
	}
	// the bridge's folder and environment come from the built sandbox
	if err := d.sandboxReady(); err != nil {
		return nil, err
	}
	command := func(name string, args ...string) (*exec.Cmd, error) {
		return d.command(context.Background(), name, args...)
	}
	env, err := d.runEnv(ctx)
	if err != nil {
		return nil, err
	}
	s, err = kernels.Start(d.ctx, command, python, env, d.Child().Dir, d.bridgeDir)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed || d.kernels != nil {
		// the door closed, or another cell started the sessions, meanwhile
		kept, closed := d.kernels, d.closed
		d.mu.Unlock()
		s.Close()
		if closed {
			return nil, errors.New("the run has ended")
		}
		return kept, nil
	}
	d.kernels = s
	d.kernelGeneration = d.Study.PythonGeneration()
	d.mu.Unlock()
	return s, nil
}

func (d *Door) runScriptLegacy(ctx context.Context, req *mcp.CallToolRequest, in runScriptLegacyIn) (*mcp.CallToolResult, any, error) {
	return d.runScript(ctx, req, runScriptIn{Path: in.Path})
}

func (d *Door) runScript(ctx context.Context, _ *mcp.CallToolRequest, in runScriptIn) (*mcp.CallToolResult, any, error) {
	if !d.OutputAttestation {
		in.Outputs = nil
	}
	if len(in.Outputs) > records.MaxScriptOutputPaths {
		return nil, nil, fmt.Errorf("run_script accepts at most %d JSON output paths", records.MaxScriptOutputPaths)
	}
	seen := make(map[string]bool, len(in.Outputs))
	for _, name := range in.Outputs {
		if !records.ValidScriptOutputPath(name) {
			return nil, nil, fmt.Errorf("%q must be a relative JSON file path under outputs/", name)
		}
		if seen[name] {
			return nil, nil, fmt.Errorf("duplicate output path %q", name)
		}
		seen[name] = true
	}
	d.executionMu.Lock()
	defer d.executionMu.Unlock()
	lang, ok := scriptLanguages[filepath.Ext(in.Path)]
	if !ok {
		return nil, nil, fmt.Errorf("%q is not a script run_script runs: it runs .py, .R and .jl files", in.Path)
	}
	_, contents, err := readScript(d.Child().Dir, in.Path)
	if err != nil {
		return nil, nil, err
	}
	if _, _, err := d.interpreter(lang); err != nil {
		return nil, nil, err
	}
	if err := d.sandboxReady(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := d.runContext(ctx)
	defer cancel()
	packages, err := d.Study.Packages(ctx, lang)
	if err != nil {
		d.recordEnvironmentError(lang, err)
		return nil, nil, fmt.Errorf("reading the study's package list: %w", err)
	}
	var root *os.Root
	var before map[string]string
	if len(in.Outputs) > 0 {
		root, err = os.OpenRoot(d.Child().Dir)
		if err != nil {
			return nil, nil, fmt.Errorf("opening the run folder to check its outputs: %w", err)
		}
		defer root.Close()
		before = scriptOutputHashes(root, in.Outputs)
	}
	output, errText, status, err := d.execScript(ctx, d.Study, in.Path, lang, string(contents))
	if err != nil {
		d.recordEnvironmentError(lang, err)
		return nil, nil, err
	}
	body := map[string]any{"tool": "run_script", "language": lang, "path": in.Path, "script": string(contents),
		"output": output, "error": errText, "exit_status": status, "packages": packages}
	if root != nil && status == 0 {
		hashes := scriptOutputHashes(root, in.Outputs)
		maps.DeleteFunc(hashes, func(name, hash string) bool { return before[name] == hash })
		body["output_hashes"] = hashes
	}
	d.clearReadyEnvironment(lang)
	if err := d.Study.CheckDefaultPython(ctx); err != nil {
		return nil, nil, err
	}
	return d.answerRun(ctx, body)
}

// execScript runs the script at path, relative to the run folder, from start
// to finish in a fresh process of language's interpreter in study, in the
// sandbox, and answers its output, its errors and its exit status, -1 when it
// ran past its time limit. script is what the file held before it ran: errors
// say so if the file changed meanwhile.
func (d *Door) execScript(ctx context.Context, study *envs.Study, path, language, script string) (output, errText string, exit int, err error) {
	dir := d.Child().Dir
	full, err := insideDir(dir, path)
	if err != nil {
		return "", "", 0, err
	}
	interpreter := study.Interpreter
	if d.interp != nil {
		interpreter = d.interp
	}
	name, args, err := interpreter(language)
	if err != nil {
		return "", "", 0, err
	}
	limit := d.ScriptTimeout
	if limit == 0 {
		limit = agent.ScriptLimit
	}
	runCtx, stop := context.WithTimeout(ctx, limit)
	defer stop()
	var stdout, stderr capped
	cmd, err := d.command(runCtx, name, append(args, full)...)
	if err != nil {
		return "", "", 0, err
	}
	cmd.Dir = dir
	cmd.Env, err = d.runEnv(runCtx)
	if err != nil {
		return "", "", 0, err
	}
	// pipes, not files: the sandbox loses some interpreters' buffered output to a file
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// the sandbox starts the script in a session of its own, whose group killGroup ends
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	// what the script left running may hold its output open
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return "", "", 0, err
	}
	cmd.Wait()
	status, errText := cmd.ProcessState.ExitCode(), stderr.String()
	if ctx.Err() != nil {
		return "", "", 0, errors.New("the run has ended")
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() && runCtx.Err() == nil {
		errText += fmt.Sprintf("\nthe script was stopped by a signal: %s\n", ws.Signal())
	}
	// The script runs from its own path, so paths relative to it work; the
	// record keeps the bytes read before, and says so if they changed.
	if _, after, err := readScript(dir, path); err != nil || sha256.Sum256(after) != sha256.Sum256([]byte(script)) {
		errText += "\n" + scriptChanged + "\n"
	}
	if runCtx.Err() != nil {
		status = -1
		// first, so a cut to fit the record keeps it
		errText = fmt.Sprintf("the script ran past its time limit of %s and was stopped\n", limitText(limit)) + errText
	}
	return stdout.String(), errText, status, nil
}

// insideDir resolves a relative path in dir, refusing one that leaves dir
// itself or through a link.
func insideDir(dir, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not inside your working folder: give a path relative to it", path)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(dir, path))
	if err != nil {
		return "", fmt.Errorf("%q: %w", path, err)
	}
	if rel, err := filepath.Rel(root, full); err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%q is not inside your working folder", path)
	}
	return full, nil
}

// readScript reads a script in dir for the run's record, refusing one that
// leaves dir. The run's code may have planted links there, so the file is
// opened without following a link, and the path is checked again after the
// open: a link swapped in for any folder on the way is refused, not read.
func readScript(dir, path string) (string, []byte, error) {
	full, err := insideDir(dir, path)
	if err != nil {
		return "", nil, err
	}
	f, err := os.OpenFile(full, os.O_RDONLY|scriptOpen, 0)
	if err != nil {
		return "", nil, fmt.Errorf("%q: %w", path, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	again, err := insideDir(dir, path)
	if err != nil {
		return "", nil, err
	}
	now, err := os.Stat(again)
	if err != nil || again != full || !os.SameFile(opened, now) || !opened.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%q changed while it was read", path)
	}
	contents, err := io.ReadAll(io.LimitReader(f, maxScript+1))
	if err != nil {
		return "", nil, err
	}
	if len(contents) > maxScript {
		return "", nil, errors.New("the script is over 256 KiB, more than a run record keeps")
	}
	return full, contents, nil
}

func limitText(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%d hours", d/time.Hour)
	}
	return d.String()
}

// answerRun records the run and answers its output under the run's key. A
// run that can't be recorded has its output withheld.
// Output is kept as the script wrote it; JSON encoding writes any bytes that
// are not UTF-8 as U+FFFD, both in the record and in the answer.
func (d *Door) answerRun(ctx context.Context, body map[string]any) (*mcp.CallToolResult, any, error) {
	fitBody(body, maxRunBody)
	key, err := d.recordRun(ctx, body)
	if err != nil {
		return nil, nil, fmt.Errorf("the run could not be recorded, so its output is withheld: %w", err)
	}
	answer := body["output"].(string)
	if e := body["error"].(string); e != "" {
		answer += "\n[stderr]\n" + e
	}
	if key != "" {
		if hashes, hasManifest := body["output_hashes"].(map[string]string); hasManifest {
			if len(hashes) == 0 {
				answer += "\n[No declared JSON output changed. Write the result to a fresh path under outputs/.]"
			} else {
				answer += fmt.Sprintf("\n[Changed JSON outputs: %q]", slices.Sorted(maps.Keys(hashes)))
			}
		}
		answer = "run:" + key + "\n" + answer
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer}}}, nil, nil
}

// fitBody cuts the error, then the output, until the encoded body fits
// limit bytes.
func fitBody(body map[string]any, limit int) {
	for _, field := range []string{"error", "output"} {
		for {
			b, _ := json.Marshal(body)
			over := len(b) - limit
			if over <= 0 {
				return
			}
			s := strings.TrimSuffix(body[field].(string), runCut)
			if s == "" {
				break
			}
			n := max(len(s)-over-len(runCut), 0)
			for n > 0 && !utf8.RuneStart(s[n]) {
				n--
			}
			body[field] = s[:n] + runCut
			if n == 0 {
				break
			}
		}
	}
}

func (d *Door) recordRun(ctx context.Context, body map[string]any) (string, error) {
	var rec struct {
		Key string `json:"key"`
	}
	api := &apiclient.Client{Server: d.Server, Token: d.CallToken}
	err := api.Do(ctx, "POST", "/api/role/runs", body, &rec)
	return rec.Key, err
}

// capped keeps the first maxAnswer bytes written to it and notes a cut.
type capped struct {
	b   strings.Builder
	cut bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := maxAnswer - c.b.Len(); len(p) > room {
		c.b.Write(p[:max(room, 0)])
		c.cut = true
	} else {
		c.b.Write(p)
	}
	return len(p), nil
}

func (c *capped) String() string {
	if c.cut {
		return c.b.String() + outputCut
	}
	return c.b.String()
}

func protectRunFolder(policy *sandbox.Policy, dir string, hide bool, placeholders *[]placeholder) error {
	for _, p := range agent.RunFolderConfig {
		if p.Hidden && !hide {
			p.Hidden = false
		}
		path := filepath.Join(dir, p.Name)
		_, err := os.Lstat(path)
		switch {
		case err == nil && p.Hidden:
			policy.Hidden = append(policy.Hidden, path)
		case err == nil:
			policy.ReadOnly = append(policy.ReadOnly, path)
		case !errors.Is(err, fs.ErrNotExist):
			return err
		case p.Marker:
			if !hide && p.Name == ".agent-process" {
				policy.LateReadOnly = append(policy.LateReadOnly, path)
			}
		case strings.Contains(p.Name, "/"):
			// Entries inside a read-only folder cannot be created by code.
		default:
			ph, err := makePlaceholder(path, p.Folder)
			if err != nil {
				return err
			}
			*placeholders = append(*placeholders, ph)
			if p.Hidden {
				policy.Hidden = append(policy.Hidden, path)
			} else {
				policy.ReadOnly = append(policy.ReadOnly, path)
			}
		}
	}
	return nil
}
