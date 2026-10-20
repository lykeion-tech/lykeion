package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/kernels"
	"lykeion/server/internal/middleware"
	"lykeion/server/pkg/agent"
)

const (
	runCodeDescription = "Run code in a live session (language: python, r or julia) that keeps its variables between calls, in this study's environment. " +
		"For exploring; a reported result must come from run_script. The answer starts with run:<key>, which you may cite as run:<key>."
	runScriptDescription = "Run a saved script (.py, .R or .jl, a path inside your working folder) from start to finish in a fresh process, in this study's environment. " +
		"Cite the result's numbers as run:<key>, the first line of the answer."
	// maxScript is the longest script or cell the server records.
	maxScript = 256 << 10
	// outputCut ends output dropped past maxAnswer, as the session bridge
	// ends a cell's.
	outputCut = "\n[output cut at 32 MiB]\n"
	// runCut ends output dropped so the record fits the server's body limit.
	runCut = "\n[output cut to fit the run record]\n"
)

// maxRunBody is the largest body the server takes on /api/role/runs.
var maxRunBody = middleware.MaxCallBodyBytes

var scriptLanguages = map[string]string{".py": envs.Python, ".R": envs.R, ".r": envs.R, ".jl": envs.Julia}

type runCodeIn struct {
	Language string `json:"language" jsonschema:"python, r or julia"`
	Code     string `json:"code" jsonschema:"the code to run in the language's session"`
}

type runScriptIn struct {
	Path string `json:"path" jsonschema:"the script's path, relative to your working folder"`
}

func (d *Door) interpreter(lang string) (string, []string, error) {
	if d.interp != nil {
		return d.interp(lang)
	}
	if d.Study == nil {
		return "", nil, errors.New("this run has no study environment")
	}
	return d.Study.Interpreter(lang)
}

// runEnv is the environment code runs with: the connectors' one, which holds
// none of the run's tokens, with the study's folders and interpreters on
// PATH ahead of the person's own.
func (d *Door) runEnv() []string {
	env := append(connectorEnv(d.Child(), nil), d.Study.Env()...)
	path := os.Getenv("PATH")
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	// A run's own PATH already carries the prefix; a child's without it gets it first.
	sep := string(os.PathListSeparator)
	if prefix := d.Study.PathPrefix(); prefix != "" && !strings.Contains(sep+path+sep, sep+prefix+sep) {
		path = prefix + sep + path
	}
	return append(env, "PATH="+path)
}

// runContext ends when the call's does or when the door closes.
func (d *Door) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (d *Door) runCode(ctx context.Context, _ *mcp.CallToolRequest, in runCodeIn) (*mcp.CallToolResult, any, error) {
	if _, _, err := d.interpreter(in.Language); err != nil {
		return nil, nil, err
	}
	if len(in.Code) > maxScript {
		return nil, nil, errors.New("the code is over 256 KiB, more than a run record keeps")
	}
	ctx, cancel := d.runContext(ctx)
	defer cancel()
	s, err := d.liveSessions()
	if err != nil {
		return nil, nil, err
	}
	packages, err := d.Study.Packages(ctx, in.Language)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the study's package list: %w", err)
	}
	res, err := s.Exec(ctx, in.Language, in.Code)
	if err != nil {
		return nil, nil, err
	}
	// a cell has no exit status of its own: one that raised counts as 1
	status := 0
	if !res.OK {
		status = 1
	}
	return d.answerRun(ctx, map[string]any{"tool": "run_code", "language": in.Language, "code": in.Code,
		"output": res.Stdout, "error": res.Error, "exit_status": status, "packages": packages})
}

// liveSessions are the run's live sessions, started on the first cell with the
// Python base's interpreter, which runs the bridge for every language.
func (d *Door) liveSessions() (*kernels.Sessions, error) {
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
	s, err = kernels.Start(d.ctx, python, d.runEnv(), d.Child().Dir)
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
	d.mu.Unlock()
	return s, nil
}

func (d *Door) runScript(ctx context.Context, _ *mcp.CallToolRequest, in runScriptIn) (*mcp.CallToolResult, any, error) {
	lang, ok := scriptLanguages[filepath.Ext(in.Path)]
	if !ok {
		return nil, nil, fmt.Errorf("%q is not a script run_script runs: it runs .py, .R and .jl files", in.Path)
	}
	dir := d.Child().Dir
	script, err := insideDir(dir, in.Path)
	if err != nil {
		return nil, nil, err
	}
	name, args, err := d.interpreter(lang)
	if err != nil {
		return nil, nil, err
	}
	contents, err := os.ReadFile(script)
	if err != nil {
		return nil, nil, err
	}
	if len(contents) > maxScript {
		return nil, nil, errors.New("the script is over 256 KiB, more than a run record keeps")
	}
	ctx, cancel := d.runContext(ctx)
	defer cancel()
	packages, err := d.Study.Packages(ctx, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the study's package list: %w", err)
	}
	limit := d.ScriptTimeout
	if limit == 0 {
		limit = agent.ScriptLimit
	}
	runCtx, stop := context.WithTimeout(ctx, limit)
	defer stop()
	var stdout, stderr capped
	cmd := exec.CommandContext(runCtx, name, append(args, script)...)
	cmd.Dir = dir
	cmd.Env = d.runEnv()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	ownGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	// what the script left running may hold its output open
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	cmd.Wait()
	status, errText := cmd.ProcessState.ExitCode(), stderr.String()
	if ctx.Err() != nil {
		return nil, nil, errors.New("the run has ended")
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() && runCtx.Err() == nil {
		errText += fmt.Sprintf("\nthe script was stopped by a signal: %s\n", ws.Signal())
	}
	if runCtx.Err() != nil {
		status = -1
		// first, so a cut to fit the record keeps it
		errText = fmt.Sprintf("the script ran past its time limit of %s and was stopped\n", limitText(limit)) + errText
	}
	return d.answerRun(ctx, map[string]any{"tool": "run_script", "language": lang, "path": in.Path, "script": string(contents),
		"output": stdout.String(), "error": errText, "exit_status": status, "packages": packages})
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
	fitBody(body)
	key, err := d.recordRun(ctx, body)
	if err != nil {
		return nil, nil, fmt.Errorf("the run could not be recorded, so its output is withheld: %w", err)
	}
	answer := body["output"].(string)
	if e := body["error"].(string); e != "" {
		answer += "\n[stderr]\n" + e
	}
	if key != "" {
		answer = "run:" + key + "\n" + answer
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer}}}, nil, nil
}

// fitBody cuts the error, then the output, until the encoded body fits
// maxRunBody.
func fitBody(body map[string]any) {
	for _, field := range []string{"error", "output"} {
		for {
			b, _ := json.Marshal(body)
			over := len(b) - maxRunBody
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
