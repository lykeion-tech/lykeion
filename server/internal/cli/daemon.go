package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"lykeion/server/internal/daemon"
)

type daemonOwner struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
	Nonce   string `json:"nonce"`
}

func stateDir(e *env) string { return filepath.Join(filepath.Dir(e.path), "daemon") }
func pidFile(e *env) string  { return filepath.Join(filepath.Dir(e.path), "daemon.pid") }
func logFile(e *env) string  { return filepath.Join(filepath.Dir(e.path), "daemon.log") }

func readOwner(path string) (daemonOwner, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return daemonOwner{}, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 4096 || fi.Mode().Perm() != 0o600 {
		return daemonOwner{}, errors.New("invalid daemon ownership file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return daemonOwner{}, err
	}
	var owner daemonOwner
	if err := json.Unmarshal(b, &owner); err != nil || owner.PID <= 0 || owner.Started == "" || owner.Nonce == "" {
		return daemonOwner{}, errors.New("invalid daemon ownership record")
	}
	return owner, nil
}

func checkLogFile(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		return errors.New("daemon log must be a private regular file")
	}
	return nil
}

func checkStateLock(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("daemon state lock is not a regular file")
	}
	return nil
}

func ownerAlive(owner daemonOwner) bool {
	if owner.PID <= 0 || owner.Started == "" || owner.Nonce == "" {
		return false
	}
	started, err := processStart(owner.PID)
	return err == nil && started == owner.Started
}

func writeOwner(path string, owner daemonOwner) error {
	if owner.PID <= 0 || owner.Started == "" || owner.Nonce == "" {
		return errors.New("invalid daemon ownership record")
	}
	dir := filepath.Dir(path)
	b, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".daemon-pid-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Write(b); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func clearOwner(path string, owner daemonOwner) error {
	current, err := readOwner(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current != owner {
		return nil
	}
	return os.Remove(path)
}

func scratchHome() (string, error) {
	root := os.Getenv("LYK_DAEMON_HOME")
	if root == "" {
		tmp, err := filepath.EvalSymlinks(os.TempDir())
		if err != nil {
			return "", err
		}
		root = filepath.Join(tmp, "lykeion-"+strconv.Itoa(os.Getuid())+"-workspaces")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("daemon scratch home must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := ownedPrivateDir(root); err != nil {
		return "", err
	}
	return root, nil
}

func daemonCmd(e *env) *cobra.Command {
	d := &cobra.Command{Use: "daemon", Short: "Run agents on this machine for your cards"}
	var slots int
	var claudeBin, codexBin, ownerNonce string
	var forDur time.Duration
	run := &cobra.Command{Use: "run", Short: "Run in the foreground", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.signedIn(); err != nil {
			return err
		}
		home, err := scratchHome()
		if err != nil {
			return fmt.Errorf("scratch home: %w", err)
		}
		h := daemon.Detect()
		if claudeBin != "" {
			bin, err := exec.LookPath(claudeBin)
			if err != nil {
				return fmt.Errorf("--claude-bin: %w", err)
			}
			h["claude"] = bin
		}
		if codexBin != "" {
			bin, err := exec.LookPath(codexBin)
			if err != nil {
				return fmt.Errorf("--codex-bin: %w", err)
			}
			h["codex"] = bin
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if forDur > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, forDur)
			defer cancel()
		}
		host, err := os.Hostname()
		if err != nil {
			return err
		}
		var owner daemonOwner
		if ownerNonce != "" {
			owner = daemonOwner{PID: os.Getpid(), Nonce: ownerNonce}
			owner.Started, err = processStart(owner.PID)
			if err != nil {
				return fmt.Errorf("identify daemon process: %w", err)
			}
		}
		cfg := daemon.Config{Server: e.cfg.Server, Token: e.cfg.Token, StateDir: stateDir(e),
			Home: home, Device: host, Harnesses: h, Slots: slots}
		if ownerNonce != "" {
			cfg.Ready = func() error { return writeOwner(pidFile(e), owner) }
			cfg.OnExit = func() {
				if err := clearOwner(pidFile(e), owner); err != nil {
					slog.Error("clear daemon ownership", "err", err)
				}
			}
		}
		return daemon.Run(ctx, cfg)
	}}
	run.Flags().IntVar(&slots, "slots", 2, "agent runs at once")
	run.Flags().StringVar(&claudeBin, "claude-bin", "", "path to claude")
	run.Flags().StringVar(&codexBin, "codex-bin", "", "path to codex")
	run.Flags().DurationVar(&forDur, "for", 0, "stop after this long")
	run.Flags().StringVar(&ownerNonce, "owner-nonce", "", "background ownership nonce")
	_ = run.Flags().MarkHidden("for")
	_ = run.Flags().MarkHidden("owner-nonce")

	start := &cobra.Command{Use: "start", Short: "Run in the background", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.signedIn(); err != nil {
			return err
		}
		return startDaemon(cmd.Context(), e)
	}}
	stop := &cobra.Command{Use: "stop", RunE: func(cmd *cobra.Command, _ []string) error {
		owner, err := readOwner(pidFile(e))
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("the daemon is not running")
		}
		if err != nil {
			return err
		}
		if !ownerAlive(owner) {
			return errors.New("daemon ownership is stale; refusing to signal this PID")
		}
		if err := terminate(owner.PID); err != nil {
			return err
		}
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		for ownerAlive(owner) {
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-deadline.C:
				return errors.New("daemon did not stop within 10 seconds")
			case <-time.After(100 * time.Millisecond):
			}
		}
		if err := clearStoppedOwner(stateDir(e), pidFile(e), owner); err != nil {
			return err
		}
		fmt.Fprintln(e.out, "Daemon stopped")
		return nil
	}}
	status := &cobra.Command{Use: "status", RunE: func(_ *cobra.Command, _ []string) error {
		owner, err := readOwner(pidFile(e))
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(e.out, "not running")
			return nil
		}
		if err != nil {
			return err
		}
		if ownerAlive(owner) {
			fmt.Fprintln(e.out, "running, pid", owner.PID)
		} else {
			fmt.Fprintln(e.out, "not running (stale ownership record)")
		}
		return nil
	}}
	logs := &cobra.Command{Use: "logs", RunE: func(_ *cobra.Command, _ []string) error {
		if err := checkLogFile(logFile(e)); err != nil {
			return err
		}
		f, err := os.Open(logFile(e))
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("no daemon log yet")
		}
		if err != nil {
			return err
		}
		defer f.Close()
		var lines [200]string
		count := 0
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lines[count%len(lines)] = scanner.Text()
			count++
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read daemon log: %w", err)
		}
		start := count - min(count, len(lines))
		for i := start; i < count; i++ {
			fmt.Fprintln(e.out, lines[i%len(lines)])
		}
		return nil
	}}
	d.AddCommand(run, start, stop, status, logs)
	return d
}

var daemonExecutable = os.Executable
var daemonChildArgs = func(nonce string) []string { return []string{"daemon", "run", "--owner-nonce", nonce} }

func startDaemon(ctx context.Context, e *env) error {
	if owner, err := readOwner(pidFile(e)); err == nil && ownerAlive(owner) {
		return fmt.Errorf("already running (pid %d)", owner.PID)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(stateDir(e), 0o700); err != nil {
		return err
	}
	if err := ownedPrivateDir(stateDir(e)); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	identity := hex.EncodeToString(nonce)
	self, err := daemonExecutable()
	if err != nil {
		return err
	}
	if _, err := processStart(os.Getpid()); err != nil {
		return fmt.Errorf("process identity is unavailable: %w", err)
	}
	if err := checkLogFile(logFile(e)); err != nil {
		return err
	}
	log, err := os.OpenFile(logFile(e), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	child := exec.Command(self, daemonChildArgs(identity)...)
	child.Stdout, child.Stderr = log, log
	if err := detach(child); err != nil {
		_ = log.Close()
		return err
	}
	if err := child.Start(); err != nil {
		_ = log.Close()
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- child.Wait() }()
	started, identityErr := processStart(child.Process.Pid)
	if err := log.Close(); err != nil {
		return errors.Join(err, stopUnreadyChild(child.Process.Pid, started, wait))
	}
	if identityErr != nil {
		select {
		case err := <-wait:
			return fmt.Errorf("daemon exited before readiness: %v (see %s)", err, logFile(e))
		case <-time.After(time.Second):
			return fmt.Errorf("cannot verify spawned daemon process: %w", identityErr)
		}
	}
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-wait:
			return fmt.Errorf("daemon exited before readiness: %v (see %s)", err, logFile(e))
		case <-deadline.C:
			return errors.Join(fmt.Errorf("daemon did not become ready within 45 seconds (see %s)", logFile(e)), stopUnreadyChild(child.Process.Pid, started, wait))
		case <-ctx.Done():
			return errors.Join(ctx.Err(), stopUnreadyChild(child.Process.Pid, started, wait))
		case <-tick.C:
			owner, err := readOwner(pidFile(e))
			if err == nil && owner.PID == child.Process.Pid && owner.Started == started && owner.Nonce == identity && ownerAlive(owner) {
				select {
				case err := <-wait:
					return fmt.Errorf("daemon exited at readiness: %v (see %s)", err, logFile(e))
				default:
				}
				fmt.Fprintln(e.out, "Daemon started, pid", owner.PID)
				return nil
			}
		}
	}
}

func stopUnreadyChild(pid int, started string, wait <-chan error) error {
	if started == "" {
		return errors.New("cannot verify child identity for shutdown")
	}
	current, err := processStart(pid)
	if err == nil && current == started {
		if err := terminate(pid); err != nil {
			return fmt.Errorf("stop unready daemon: %w", err)
		}
	} else if err == nil {
		return errors.New("unready daemon PID was reused; refusing to signal")
	}
	select {
	case <-wait:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("unready daemon did not exit after shutdown signal")
	}
}
