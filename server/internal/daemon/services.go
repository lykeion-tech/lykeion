package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/text/unicode/norm"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"lykeion/server/internal/apiclient"
	studyarchive "lykeion/server/internal/archive"
	"lykeion/server/internal/configpath"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

const serviceLogBytes = 64 << 10

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type runningService struct {
	mu     sync.Mutex
	exited bool
	cmd    *exec.Cmd
	done   chan struct{}
	log    *serviceLog
	tmp    string
}

func (s *runningService) terminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exited {
		terminateService(s.cmd)
	}
}
func (s *runningService) kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exited {
		killGroup(s.cmd)
	}
}
func (s *runningService) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited && serviceGroupAlive(s.cmd)
}

type runningServices struct {
	children  []*runningService
	env       []string
	stopLimit time.Duration
	once      sync.Once
}

func (s *runningServices) Env() []string { return slices.Clone(s.env) }
func (s *runningServices) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		for _, child := range s.children {
			child.terminate()
		}
		deadline := time.Now().Add(s.stopLimit)
		for _, child := range s.children {
			for child.alive() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			child.kill()
			<-child.done
			os.RemoveAll(child.tmp)
		}
	})
}

func (d *daemon) startServices(ctx context.Context, c claimed, study *envs.Study) (out *runningServices, err error) {
	out = &runningServices{stopLimit: 10 * time.Second}
	if d.serviceStopLimit > 0 {
		out.stopLimit = d.serviceStopLimit
	}
	if c.AgentRun.BoardColumn != "experimentation" || len(c.Services) == 0 {
		return out, nil
	}
	defer func() {
		if err != nil {
			out.Stop()
			out = nil
		}
	}()
	seen := map[string]bool{}
	clients := map[string]bool{}
	for _, spec := range c.Services {
		failure := fmt.Errorf("hidden service %s did not start; see the daemon log", spec.Name)
		if len(c.Services) > 4 || study == nil || c.AgentRun.CardID == nil || d.sandboxErr != nil || !serviceName.MatchString(spec.Name) || seen[spec.Name] {
			return out, failure
		}
		seen[spec.Name] = true
		if spec.Client != "" {
			key := strings.ToLower(norm.NFC.String(filepath.Base(spec.Client)))
			if clients[key] {
				slog.Error("hidden service did not start", "service", spec.Name, "error", "duplicate hidden service client filename")
				return out, failure
			}
			clients[key] = true
		}
	}
	for _, spec := range c.Services {
		failure := fmt.Errorf("hidden service %s did not start; see the daemon log", spec.Name)
		child, url, startErr := d.startService(ctx, c, study, spec)
		if child != nil {
			out.children = append(out.children, child)
		}
		if startErr != nil {
			// A service's diagnostics belong only to this machine's daemon log.
			tail := ""
			if child != nil && child.log != nil {
				tail = child.log.String()
			}
			slog.Error("hidden service did not start", "service", spec.Name, "error", startErr, "tail", tail)
			return out, failure
		}
		out.env = append(out.env, "LYK_SERVICE_"+strings.ToUpper(strings.ReplaceAll(spec.Name, "-", "_"))+"_URL="+url)
	}
	return out, nil
}

func (d *daemon) startService(ctx context.Context, c claimed, study *envs.Study, spec service.ClaimedService) (child *runningService, url string, err error) {
	if runtime.GOOS == "darwin" && d.serviceCommand == nil {
		return nil, "", sandbox.ErrLoopbackOnlyUnsupported
	}
	root := filepath.Join(d.cfg.StateDir, "services", c.AgentRun.WorkspaceID.String(), c.AgentRun.CardID.String(), spec.Name)
	if err = os.MkdirAll(root, 0700); err != nil {
		return
	}
	code := filepath.Join(root, "code")
	if err = d.prepareServiceCode(ctx, c, spec, root); err != nil {
		return
	}
	if spec.Client != "" {
		if err = publishServiceClient(ctx, code, spec.Client, study.Dir); err != nil {
			return
		}
	}
	state := filepath.Join(root, "state")
	if err = os.MkdirAll(state, 0700); err != nil {
		return
	}
	tmp, e := os.MkdirTemp("", "lyk-service-")
	if e != nil {
		err = e
		return
	}
	defer func() {
		if child == nil {
			os.RemoveAll(tmp)
		}
	}()
	policy, e := servicePolicy(d.cfg.StateDir, d.cfg.Home, code, state, tmp, study.Bases)
	if e != nil {
		err = e
		return
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		err = e
		return
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	command := sandbox.Policy.Command
	if d.serviceCommand != nil {
		command = d.serviceCommand
	}
	cmd, e := command(policy, ctx, "/bin/sh", "-c", spec.Start)
	if e != nil {
		err = e
		return
	}
	cmd.Dir = code
	cmd.Env = serviceEnvironment(c.Env, spec.Env, port, state, tmp, study.Bases)
	log := &serviceLog{path: filepath.Join(root, "log")}
	logFile, e := os.OpenFile(log.path, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		err = e
		return
	}
	if e = logFile.Close(); e != nil {
		err = e
		return
	}
	cmd.Stdout = log
	cmd.Stderr = log
	// Stop owns escalation for the entire group, including grandchildren.
	cmd.Cancel = func() error { terminateService(cmd); return nil }
	cmd.WaitDelay = 10 * time.Second
	if d.serviceStopLimit > 0 {
		cmd.WaitDelay = d.serviceStopLimit
	}
	if e = cmd.Start(); e != nil {
		err = e
		return
	}
	child = &runningService{cmd: cmd, done: make(chan struct{}), log: log, tmp: tmp}
	go func() {
		cmd.Wait()
		child.mu.Lock()
		// A finished leader cannot leave background services behind or cause a
		// later Stop to signal a process group whose ID has since been reused.
		killGroup(cmd)
		child.exited = true
		close(child.done)
		child.mu.Unlock()
	}()
	limit := 60 * time.Second
	if d.serviceReadyLimit > 0 {
		limit = d.serviceReadyLimit
	}
	ready, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	address := net.JoinHostPort("127.0.0.1", port)
	for {
		select {
		case <-child.done:
			err = errors.New("service exited before readiness")
			return
		default:
		}
		conn, dialErr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ready, "tcp4", address)
		if dialErr == nil {
			conn.Close()
			select {
			case <-child.done:
				err = errors.New("service exited before readiness")
				return
			default:
			}
			return child, "http://" + address, nil
		}
		select {
		case <-ready.Done():
			err = ready.Err()
			return
		case <-child.done:
			err = errors.New("service exited before readiness")
			return
		case <-ticker.C:
		}
	}
}

// servicePolicy grants no study, run, personal config or daemon credential reads.
func servicePolicy(stateRoot, scratchRoot, code, state, tmp string, bases *envs.Bases) (sandbox.Policy, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sandbox.Policy{}, err
	}
	p := sandbox.Policy{Home: home, Read: []string{code}, Write: []string{state, tmp}, LoopbackOnly: true}
	private, err := configpath.Private()
	if err != nil {
		return p, err
	}
	p.Hidden = private
	for _, root := range []string{stateRoot, scratchRoot, bases.Dir} {
		if root == "" || slices.Contains(p.RestrictedRoots, root) {
			continue
		}
		if err := sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return p, err
		}
		p.RestrictedRoots = append(p.RestrictedRoots, root)
	}
	if stateRoot != "" {
		p.Hidden = append(p.Hidden, filepath.Join(filepath.Dir(stateRoot), "daemon.log"))
	}

	if err = bases.EnsureLeanRoot(); err != nil {
		return p, err
	}
	for _, base := range bases.Folders() {
		if isDir(base) {
			p.Read = append(p.Read, base)
		}
	}
	return p, nil
}
func serviceEnvironment(study map[string]string, names []string, port, state, tmp string, bases *envs.Bases) []string {
	paths := []string{}
	for _, base := range bases.Folders() {
		if isDir(base) {
			paths = append(paths, filepath.Join(base, "bin"))
		}
	}
	paths = append(paths, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	env := []string{"PATH=" + strings.Join(paths, string(os.PathListSeparator)), "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "HOME=" + tmp, "LYK_SERVICE_PORT=" + port, "LYK_SERVICE_STATE=" + state}
	accepted := acceptedStudyEnv(study)
	named := map[string]string{}
	for _, name := range names {
		if value, ok := accepted[name]; ok {
			named[name] = value
		}
	}
	return mergeEnv(env, named)
}

func (d *daemon) prepareServiceCode(ctx context.Context, c claimed, spec service.ClaimedService, root string) error {
	// Immutable uploads can be shared by simultaneous runs without replacing a
	// live process's code. Only initialization holds this publication lock.
	unlock, err := lockServiceCode(ctx, root)
	if err != nil {
		return err
	}
	defer unlock()
	if spec.SizeBytes < 0 || spec.SizeBytes > 5<<30 {
		return errors.New("invalid service archive size")
	}
	digest, err := hex.DecodeString(spec.Sha256)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("invalid service archive digest")
	}
	code := filepath.Join(root, "code")
	marker := filepath.Join(root, "code.sha256")
	if b, err := os.ReadFile(marker); err == nil {
		fi, statErr := os.Lstat(code)
		if string(b) == spec.Sha256 && statErr == nil && fi.IsDir() {
			return nil
		}
		return errors.New("service code does not match its immutable upload")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// No child can launch before the marker is published. Unmarked code and
	// extraction partials therefore belong to an interrupted initialization.
	for _, partial := range []string{code + ".part", code} {
		if err := agent.RemoveTree(partial); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(root, "download-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	ctx, cancel := context.WithTimeout(ctx, middleware.AttachmentRequestTimeout)
	defer cancel()
	api := apiclient.Client{Server: d.cfg.Server, Token: d.cfg.Token, Workspace: c.AgentRun.WorkspaceID.String(), HTTP: &http.Client{}}
	if d.api != nil {
		api.Server = d.api.Server
		api.Token = d.api.Token
		if d.api.HTTP != nil {
			h := *d.api.HTTP
			h.Timeout = 0
			api.HTTP = &h
		}
	}
	hash := sha256.New()
	writer := &capWriter{w: io.MultiWriter(tmp, hash), left: spec.SizeBytes}
	if err = api.Download(ctx, "/api/daemon/runs/"+c.AgentRun.ID.String()+"/services/"+spec.ID.String(), writer); err != nil {
		return err
	}
	if writer.left != 0 || hex.EncodeToString(hash.Sum(nil)) != spec.Sha256 {
		return errors.New("service bytes do not match size and digest")
	}
	head := make([]byte, 512)
	n, err := tmp.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return err
	}
	format := studyarchive.Kind(head[:n], "")
	listing, err := studyarchive.ListContext(ctx, tmp.Name(), format)
	if err != nil {
		return err
	}
	// The daemon repeats all bounds from the verified blob; zero remains strict.
	if err = studyarchive.Extract(ctx, tmp.Name(), format, code, listing.Bytes, listing.Files); err != nil {
		return err
	}
	if err = filepath.WalkDir(code, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chmod(path, 0555)
	}); err != nil {
		return errors.Join(err, agent.RemoveTree(code))
	}
	if err = os.WriteFile(marker, []byte(spec.Sha256), 0600); err != nil {
		return errors.Join(err, agent.RemoveTree(code))
	}
	return nil
}

func publishServiceClient(ctx context.Context, code, client, study string) error {
	unlock, err := lockServiceCode(ctx, filepath.Join(study, "bin", filepath.Base(client)))
	if err != nil {
		return err
	}
	defer unlock()
	if !filepath.IsLocal(client) {
		return errors.New("invalid public client path")
	}
	source, err := os.OpenRoot(code)
	if err != nil {
		return err
	}
	defer source.Close()
	in, err := source.Open(client)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("public client is not a regular file")
	}
	root, err := os.OpenRoot(study)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.Mkdir("bin", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	// The directory handle stays confined even if running study code swaps bin.
	bin, err := root.OpenRoot("bin")
	if err != nil {
		return err
	}
	defer bin.Close()
	name := filepath.Base(client)
	if fi, err := bin.Lstat(name); err == nil {
		if !fi.Mode().IsRegular() {
			return errors.New("public client target is not a regular file")
		}
		if err = bin.Remove(name); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := bin.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0555)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		bin.Remove(name)
	}
	return err
}

var serviceCodeLocks = struct {
	sync.Mutex
	entries map[string]*serviceCodeLock
}{entries: map[string]*serviceCodeLock{}}

type serviceCodeLock struct {
	held  chan struct{}
	users int
}

func lockServiceCode(ctx context.Context, path string) (func(), error) {
	serviceCodeLocks.Lock()
	lock := serviceCodeLocks.entries[path]
	if lock == nil {
		lock = &serviceCodeLock{held: make(chan struct{}, 1)}
		serviceCodeLocks.entries[path] = lock
	}
	lock.users++
	serviceCodeLocks.Unlock()
	drop := func() {
		serviceCodeLocks.Lock()
		lock.users--
		if lock.users == 0 {
			delete(serviceCodeLocks.entries, path)
		}
		serviceCodeLocks.Unlock()
	}
	select {
	case lock.held <- struct{}{}:
		return func() { <-lock.held; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

// serviceLog retains a bounded private tail, including when a service floods
// stderr. The file and each run's diagnostic tail contain at most 64 KiB.
type serviceLog struct {
	mu   sync.Mutex
	tail []byte
	path string
}

func (l *serviceLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if len(p) >= serviceLogBytes {
		l.tail = append(l.tail[:0], p[len(p)-serviceLogBytes:]...)
	} else {
		over := len(l.tail) + len(p) - serviceLogBytes
		if over > 0 {
			copy(l.tail, l.tail[over:])
			l.tail = l.tail[:len(l.tail)-over]
		}
		l.tail = append(l.tail, p...)
	}
	unlock := lockAttachment(l.path)
	defer unlock()
	previous, err := os.ReadFile(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return n, err
	}
	if len(p) >= serviceLogBytes {
		previous = p[len(p)-serviceLogBytes:]
	} else {
		if len(previous) > serviceLogBytes-len(p) {
			previous = previous[len(previous)-(serviceLogBytes-len(p)):]
		}
		previous = append(previous, p...)
	}
	return n, os.WriteFile(l.path, previous, 0600)
}
func (l *serviceLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return string(l.tail) }
