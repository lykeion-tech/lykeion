package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

var recordedSetupLimit = 30 * time.Minute

const recordedNotReady = "set up the project environment first (lyk env set-up)"
const recordedChanged = "the original run environment is unavailable; restore it before rerunning"

type recordedTail struct {
	mu    sync.Mutex
	data  []byte
	limit int
	total int64
}

func (t *recordedTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	t.total += int64(n)
	if len(p) >= t.limit {
		t.data = append(t.data[:0], p[len(p)-t.limit:]...)
	} else {
		if extra := len(t.data) + len(p) - t.limit; extra > 0 {
			t.data = t.data[extra:]
		}
		t.data = append(t.data, p...)
	}
	return n, nil
}
func (t *recordedTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.data), "?")
}
func recordedEnv(s *envs.Study, additions []string, tmp string, seed int64) []string {
	env := []string{"HOME=" + filepath.Join(tmp, "home"), "LANG=C.UTF-8", "PATH=" + s.PathPrefix() + ":/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONHASHSEED=0", "PYTHONDONTWRITEBYTECODE=1", "LYK_SEED=" + strconv.FormatInt(seed, 10)}
	env = append(env, coreTmpEnv(tmp)...)
	for _, e := range additions {
		k, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, "LYK_") || k == "HOME" || k == "PATH" || k == "PYTHONHASHSEED" {
			continue
		}
		env = append(env, e)
	}
	return env
}
func recordedLang(path string) (string, error) {
	switch filepath.Ext(path) {
	case ".py":
		return envs.Python, nil
	case ".R", ".r":
		return envs.R, nil
	case ".jl":
		return envs.Julia, nil
	}
	return "", errors.New("scripts must be .py, .R or .jl")
}
func (d *daemon) recordedStudy(ctx context.Context, w coresearch.WorkContext) (*envs.Study, error) {
	if err := d.checkChat(ctx, w); err != nil {
		return nil, err
	}
	if d.bases == nil {
		return nil, errors.New(recordedNotReady)
	}
	dir := filepath.Join(d.bases.Dir, "coresearch", w.WorkspaceID.String(), w.ProjectID.String())
	for _, p := range []string{filepath.Join(d.bases.Dir, "coresearch"), filepath.Dir(dir), dir} {
		if err := privateDir(p); err != nil {
			return nil, err
		}
	}
	s := envs.ForStudy(d.bases, dir)
	if w.PythonVersion != nil && *w.PythonVersion != "" {
		if err := s.RememberPython(*w.PythonVersion); err != nil {
			return nil, err
		}
	}
	s.PythonVersion = s.RecordedPython()
	return s.ForRun(), nil
}
func (d *daemon) checkRecorded(ctx context.Context, w coresearch.WorkContext, kind string, id uuid.UUID) error {
	if err := d.checkChat(ctx, w); err != nil {
		return err
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := d.coreAPI(w.WorkspaceID).Do(ctx, "GET", d.coreWorkPath(w, kind+"/"+id.String()+"/status"), nil, &status); err != nil {
		return err
	}
	if status.Status != "running" {
		return errors.New("Coresearch work is no longer running")
	}
	return nil
}
func (d *daemon) recordedPolicy(s *envs.Study, dir, tmp string, setup bool) (sandbox.Policy, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sandbox.Policy{}, err
	}
	private, err := d.privatePaths()
	if err != nil {
		return sandbox.Policy{}, err
	}
	roots := []string{d.cfg.Home, s.Bases.Dir}
	for _, root := range roots {
		if err = sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return sandbox.Policy{}, err
		}
	}
	p := codePolicy(home, dir, s, tmp, private, roots)
	p.StrictRead = true
	if !setup {
		p.NoNetwork = true
		p.Write = []string{filepath.Join(dir, "out"), tmp}
		p.Read = append(p.Read, s.Dir, filepath.Join(dir, "in"), filepath.Join(dir, "script"))
	}
	return p, nil
}
func (d *daemon) recordedRunner(w coresearch.WorkContext, kind string, id uuid.UUID, s *envs.Study, p sandbox.Policy, tmp string, seed int64, log *recordedTail) func(context.Context, []string, string, string, ...string) ([]byte, error) {
	return func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if err := d.checkRecorded(ctx, w, kind, id); err != nil {
			return nil, err
		}
		cmd, err := p.Command(ctx, name, args...)
		if err != nil {
			return nil, err
		}
		defer sandbox.CloseCommand(cmd)
		cmd.Dir = dir
		cmd.Env = recordedEnv(s, env, tmp, seed)
		cmd.Cancel = func() error { killGroup(cmd); return nil }
		cmd.WaitDelay = 5 * time.Second
		tail := &recordedTail{limit: 4 << 20}
		var out io.Writer = tail
		if log != nil {
			out = io.MultiWriter(tail, log)
		}
		cmd.Stdout = out
		cmd.Stderr = out
		err = cmd.Run()
		killGroup(cmd)
		if log == nil && tail.total > int64(tail.limit) {
			return nil, errors.New("environment inventory exceeds 4 MiB")
		}
		return []byte(tail.String()), err
	}
}
func recordedEnvSHA(ctx context.Context, s *envs.Study) (string, error) { return s.Fingerprint(ctx) }
func (d *daemon) restoreRecorded(ctx context.Context, c coresearch.ClaimedRun, dir string) error {
	refs := append([]coresearch.FileRef{c.Script}, c.Inputs...)
	seen := map[string]bool{}
	for i, ref := range refs {
		path, err := coresearch.CleanPath(ref.Path)
		if err != nil || path != ref.Path {
			return fmt.Errorf("invalid saved path %q", ref.Path)
		}
		digest, err := hex.DecodeString(ref.SHA256)
		if err != nil || len(digest) != 32 || ref.Size < 0 || ref.Size > 50<<20 || ref.N <= 0 {
			return errors.New("invalid saved file reference")
		}
		if i > 0 {
			path = filepath.Join("in", path)
		} else {
			path = filepath.Join("script", path)
		}
		if seen[path] {
			return fmt.Errorf("duplicate recorded input %q", path)
		}
		seen[path] = true
		dest := filepath.Join(dir, path)
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		hash := sha256.New()
		bounded := &fillWriter{w: io.MultiWriter(f, hash), left: ref.Size}
		err = d.coreAPI(c.WorkspaceID).Download(ctx, d.corePath("blobs/"+ref.SHA256), bounded)
		if err == nil && (bounded.left != 0 || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256) {
			err = errors.New("saved blob size or SHA256 mismatch")
		}
		if err == nil {
			err = f.Chmod(0400)
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return err
		}
	}
	return nil
}

type recordedOutput struct {
	path, sha string
	file      string
	size      int64
}

func closeRecordedOutputs(outputs []recordedOutput) {
	for _, o := range outputs {
		os.Remove(o.file)
	}
}
func collectRecordedOutputs(dir string) (outputs []recordedOutput, skipped map[string]string, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	skipped = map[string]string{}
	var walk func(*os.Root, string) error
	walk = func(r *os.Root, prefix string) error {
		f, e := r.Open(".")
		if e != nil {
			return e
		}
		entries, e := f.ReadDir(-1)
		f.Close()
		if e != nil {
			return e
		}
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.ToSlash(filepath.Join(prefix, name))
			clean, e := coresearch.CleanPath(path)
			if e != nil || clean != path {
				skipped[path] = "invalid path"
				continue
			}
			info, e := r.Lstat(name)
			if e != nil {
				skipped[path] = e.Error()
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				skipped[path] = "symbolic link"
				continue
			}
			if info.IsDir() {
				child, e := r.OpenRoot(name)
				if e != nil {
					skipped[path] = e.Error()
					continue
				}
				opened, openErr := child.Stat(".")
				current, currentErr := r.Lstat(name)
				if openErr != nil || currentErr != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !os.SameFile(info, current) {
					child.Close()
					skipped[path] = "directory changed during collection"
					continue
				}
				e = walk(child, path)
				child.Close()
				if e != nil {
					return e
				}
				continue
			}
			if !info.Mode().IsRegular() {
				skipped[path] = "not a regular file"
				continue
			}
			if info.Size() > 50<<20 {
				skipped[path] = "over 50 MiB"
				continue
			}
			input, e := r.OpenFile(name, os.O_RDONLY|scriptOpen, 0)
			if e != nil {
				skipped[path] = e.Error()
				continue
			}
			actual, e := input.Stat()
			if e != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
				input.Close()
				skipped[path] = "file changed during collection"
				continue
			}
			spool, e := os.CreateTemp("", "lyk-recorded-output-")
			if e != nil {
				input.Close()
				return e
			}
			hash := sha256.New()
			n, e := io.Copy(io.MultiWriter(spool, hash), io.LimitReader(input, (50<<20)+1))
			input.Close()
			if e != nil || n > 50<<20 {
				spool.Close()
				os.Remove(spool.Name())
				if n > 50<<20 {
					skipped[path] = "over 50 MiB"
				} else {
					skipped[path] = e.Error()
				}
				continue
			}
			if e = spool.Close(); e != nil {
				spool.Close()
				os.Remove(spool.Name())
				return e
			}
			outputs = append(outputs, recordedOutput{path, hex.EncodeToString(hash.Sum(nil)), spool.Name(), n})
		}
		return nil
	}
	err = walk(root, "")
	if err != nil {
		closeRecordedOutputs(outputs)
		return nil, skipped, err
	}
	slices.SortFunc(outputs, func(a, b recordedOutput) int { return strings.Compare(a.path, b.path) })
	return
}

func (d *daemon) finishRecorded(ctx context.Context, c coresearch.ClaimedRun, facts map[string]any, outputs []recordedOutput) error {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		err := form.WriteField("facts", string(mustMarshal(facts)))
		for _, out := range outputs {
			if err != nil {
				break
			}
			var part io.Writer
			part, err = form.CreateFormFile("out/"+out.path, filepath.Base(out.path))
			if err == nil {
				var snapshot *os.File
				snapshot, err = coresearch.OpenSnapshot(out.file, out.size, out.sha)
				if err == nil {
					_, err = io.Copy(part, snapshot)
					err = errors.Join(err, snapshot.Close())
				}
			}
		}
		if err == nil {
			err = form.Close()
		}
		writer.CloseWithError(err)
		done <- err
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(d.api.Server, "/")+d.coreWorkPath(c.WorkContext, "runs/"+c.ID.String()+"/finish"), reader)
	if err != nil {
		reader.CloseWithError(err)
		<-done
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.api.Token)
	req.Header.Set("X-Workspace-ID", c.WorkspaceID.String())
	req.Header.Set("X-Daemon-ID", d.id)
	req.Header.Set("Content-Type", form.FormDataContentType())
	client := http.Client{Timeout: 5 * time.Minute}
	if d.api.HTTP != nil {
		client = *d.api.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	reader.Close()
	writeErr := <-done
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return &apiclient.HTTPError{Status: resp.StatusCode, Msg: "recorded-run settlement refused"}
	}
	return writeErr
}

func (d *daemon) dispatchCoresearchRun(parent context.Context, c coresearch.ClaimedRun) (result error) {
	if err := d.journalCoresearch(c.WorkContext, "runs", c.ID); err != nil {
		return err
	}
	if err := d.checkRecorded(parent, c.WorkContext, "runs", c.ID); err != nil {
		_, sendErr := d.queueCoresearch(c.WorkContext, "runs", c.ID, coreFailure("runs", err), nil)
		return errors.Join(err, sendErr)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	watched := make(chan struct{})
	go func() { defer close(watched); d.watchCoresearch(ctx, cancel, c.WorkContext, "runs", c.ID) }()
	defer func() { cancel(); <-watched }()
	facts := map[string]any{"status": "failed", "exit_status": -1, "wall_ms": int64(0), "stdout_tail": "", "stderr_tail": "", "outputs": map[string]string{}, "env_fingerprint_version": envs.FingerprintVersion}
	var outputs []recordedOutput
	var runDir string
	durable := true
	defer func() {
		if durable {
			closeRecordedOutputs(outputs)
		}
		if runDir != "" {
			_ = os.RemoveAll(runDir)
		}
	}()
	defer func() {
		if result != nil {
			facts["error"] = result.Error()
		}
		var err error
		durable, err = d.queueCoresearch(c.WorkContext, "runs", c.ID, facts, outputs)
		result = errors.Join(result, err)
	}()
	if c.ExpectedEnvSHA != nil && c.ExpectedEnvVersion != envs.FingerprintVersion {
		return errors.New(recordedChanged + " (incompatible fingerprint format)")
	}
	lang, err := recordedLang(c.Script.Path)
	if err != nil {
		return err
	}
	s, err := d.recordedStudy(ctx, c.WorkContext)
	if err != nil {
		return err
	}
	release, err := s.ExecutionLease(ctx)
	if err != nil {
		return err
	}
	defer release()
	s.ReadOnly = true
	if err = s.Ready(lang); err != nil {
		return errors.New(recordedNotReady)
	}
	base := filepath.Join(d.cfg.Home, "coresearch-runs")
	if err = privateDir(base); err != nil {
		return err
	}
	dir := filepath.Join(base, c.ID.String())
	if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
	runDir = dir
	tmp := filepath.Join(dir, "tmp")
	for _, p := range []string{"in", "out", "script", "tmp", "tmp/home"} {
		if err = os.Mkdir(filepath.Join(dir, p), 0700); err != nil {
			return err
		}
	}
	if err = d.restoreRecorded(ctx, c, dir); err != nil {
		return err
	}
	p, err := d.recordedPolicy(s, dir, tmp, false)
	if err != nil {
		return err
	}
	s.Run = d.recordedRunner(c.WorkContext, "runs", c.ID, s, p, tmp, c.Seed, nil)
	before, err := recordedEnvSHA(ctx, s)
	if err != nil {
		return err
	}
	facts["env_sha"] = before
	if c.ExpectedEnvSHA != nil && before != *c.ExpectedEnvSHA {
		return errors.New(recordedChanged)
	}
	environment, err := s.Environment(ctx)
	if err != nil {
		return err
	}
	name, args, err := s.Interpreter(lang)
	if err != nil {
		return err
	}
	runCtx, stop := context.WithTimeout(ctx, agent.ScriptLimit)
	defer stop()
	if err = d.checkRecorded(runCtx, c.WorkContext, "runs", c.ID); err != nil {
		return err
	}
	cmd, err := p.Command(runCtx, name, append(args, filepath.Join(dir, "script", c.Script.Path))...)
	if err != nil {
		return err
	}
	defer sandbox.CloseCommand(cmd)
	cmd.Dir = dir
	cmd.Env = recordedEnv(s, environment, tmp, c.Seed)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &recordedTail{limit: 16 << 10}, &recordedTail{limit: 16 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	start := time.Now()
	runErr := cmd.Run()
	killGroup(cmd)
	facts["wall_ms"] = time.Since(start).Milliseconds()
	facts["stdout_tail"] = stdout.String()
	facts["stderr_tail"] = stderr.String()
	if cmd.ProcessState != nil {
		facts["exit_status"] = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	after, err := recordedEnvSHA(ctx, s)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("the project environment changed during execution")
	}
	var skipped map[string]string
	outputs, skipped, err = collectRecordedOutputs(filepath.Join(dir, "out"))
	if err != nil {
		return err
	}
	facts["skipped"] = skipped
	hashes := map[string]string{}
	for _, o := range outputs {
		hashes[o.path] = o.sha
	}
	facts["outputs"] = hashes
	if runErr != nil {
		return runErr
	}
	facts["status"] = "ok"
	return nil
}

func (d *daemon) dispatchCoresearchSetup(parent context.Context, c coresearch.ClaimedSetup) (result error) {
	if err := d.journalCoresearch(c.WorkContext, "setups", c.ID); err != nil {
		return err
	}
	if err := d.checkRecorded(parent, c.WorkContext, "setups", c.ID); err != nil {
		_, sendErr := d.queueCoresearch(c.WorkContext, "setups", c.ID, coreFailure("setups", err), nil)
		return errors.Join(err, sendErr)
	}
	ctx, cancel := context.WithCancel(parent)
	setupCtx, stop := context.WithTimeout(ctx, recordedSetupLimit)
	defer stop()
	defer cancel()
	watched := make(chan struct{})
	go func() { defer close(watched); d.watchCoresearch(ctx, cancel, c.WorkContext, "setups", c.ID) }()
	defer func() { cancel(); <-watched }()
	log := &recordedTail{limit: 64 << 10}
	version := ""
	defer func() {
		if result != nil {
			log.Write([]byte("\n" + result.Error()))
		}
		body := map[string]string{"status": "ok", "log": log.String()}
		if result != nil {
			body["status"] = "failed"
		}
		if version != "" {
			body["python_version"] = version
		}
		_, err := d.queueCoresearch(c.WorkContext, "setups", c.ID, body, nil)
		result = errors.Join(result, err)
	}()
	s, err := d.recordedStudy(setupCtx, c.WorkContext)
	if err != nil {
		return err
	}
	release, err := s.ExecutionLease(setupCtx)
	if err != nil {
		return err
	}
	defer release()
	version = s.RecordedPython()
	s.PersistPython = func(ctx context.Context, v string) error {
		if err := d.checkRecorded(ctx, c.WorkContext, "setups", c.ID); err != nil {
			return err
		}
		if err := d.coreAPI(c.WorkspaceID).Do(ctx, "POST", d.coreWorkPath(c.WorkContext, "setups/"+c.ID.String()+"/finish"), map[string]string{"status": "running", "python_version": v}, nil); err != nil {
			return err
		}
		version = v
		return nil
	}
	tmp, err := os.MkdirTemp("", "lyk-coresearch-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err = os.Mkdir(filepath.Join(tmp, "home"), 0700); err != nil {
		return err
	}
	p, err := d.recordedPolicy(s, s.Dir, tmp, true)
	if err != nil {
		return err
	}
	s.Run = d.recordedRunner(c.WorkContext, "setups", c.ID, s, p, tmp, 0, log)
	python := s.RecordedPython()
	if c.Python != nil && *c.Python != "" {
		python = *c.Python
	}
	if python == "" {
		python = envs.DefaultPythonVersion
	}
	if !envs.ValidPythonVersion(python) {
		return errors.New("python must be a version from 3.9 to 3.14")
	}
	if old := s.RecordedPython(); old != "" && old != python {
		return envs.PythonConflict(old)
	}
	if err = s.PersistPython(setupCtx, python); err != nil {
		return err
	}
	if err = s.RememberPython(python); err != nil {
		return err
	}
	_, err = s.SetUpConda(setupCtx, python, c.Conda, "")
	if err == nil {
		_, err = s.Environment(setupCtx)
	}
	return err
}
