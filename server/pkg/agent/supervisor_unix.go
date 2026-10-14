//go:build unix

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/processidentity"
)

type processOwner struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
	Nonce   string `json:"nonce"`
}

// The supervisor owns the group until it has cleaned credentials and recorded
// its child's status. It kills its own group, including any leaked descendants.
const supervisorScript = `nonce=$1
marker=$2
shift 2
cleanup() { /bin/rm -f -- "$PWD/.cli-home/.credentials.json" "$PWD/.cli-home/auth.json"; }
if ! IFS= read -r permit <&3; then cleanup; exit 125; fi
exec 3<&-
"$@" <&0 &
child=$!
wait "$child"
result=$?
cleanup || result=125
(umask 077; printf '%s %s\n' "$nonce" "$result" > "$marker.tmp") && /bin/mv -f -- "$marker.tmp" "$marker" || exit 125
kill -KILL -$$
`

func supervised(ctx context.Context, original *exec.Cmd) (*exec.Cmd, *os.File, *os.File, string, error) {
	if original.Err != nil {
		return nil, nil, nil, "", original.Err
	}
	candidate := original.Path
	if !filepath.IsAbs(candidate) && strings.ContainsRune(candidate, os.PathSeparator) {
		candidate = filepath.Join(original.Dir, candidate)
	}
	if _, err := exec.LookPath(candidate); err != nil {
		return nil, nil, nil, "", err
	}
	nonce := uuid.NewString()
	marker := filepath.Join(original.Dir, ".agent-exit")
	for _, p := range []string{marker, marker + ".tmp"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, "", err
		}
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, "", err
	}
	args := []string{"-c", supervisorScript, "lykeion-agent", nonce, marker, original.Path}
	args = append(args, original.Args[1:]...)
	cmd := exec.CommandContext(ctx, "/bin/sh", args...)
	cmd.Dir, cmd.Env, cmd.Stdin = original.Dir, original.Env, original.Stdin
	cmd.ExtraFiles = []*os.File{reader}
	return cmd, reader, writer, nonce, nil
}
func recordProcess(cmd *exec.Cmd, nonce string) error {
	start, err := processidentity.Start(cmd.Process.Pid)
	if err != nil {
		return err
	}
	owner := processOwner{PID: cmd.Process.Pid, Started: start, Nonce: nonce}
	group, err := syscall.Getpgid(owner.PID)
	if err != nil {
		return err
	}
	if group != owner.PID {
		return errors.New("agent is not process group leader")
	}
	b, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(cmd.Dir, ".agent-process-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(cmd.Dir, ".agent-process")); err != nil {
		return err
	}
	d, err := os.Open(cmd.Dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
func readProcess(dir string) (processOwner, error) {
	var owner processOwner
	p := filepath.Join(dir, ".agent-process")
	fi, err := os.Lstat(p)
	if err != nil {
		return owner, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 4096 || fi.Mode().Perm() != 0600 {
		return owner, errors.New("invalid agent process ownership file")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return owner, err
	}
	err = json.Unmarshal(b, &owner)
	if err != nil || owner.PID <= 1 || owner.Started == "" || owner.Nonce == "" {
		return owner, errors.New("invalid agent process ownership")
	}
	return owner, nil
}
func sameProcess(owner processOwner) (bool, error) {
	start, err := processidentity.Start(owner.PID)
	if err != nil {
		// macOS can return EIO when kern.proc.pid disappears during the read.
		// Confirm absence independently instead of treating arbitrary IO errors as death.
		_, groupErr := syscall.Getpgid(owner.PID)
		if errors.Is(groupErr, syscall.ESRCH) {
			return false, nil
		}
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if start != owner.Started {
		return false, nil
	}
	return true, nil
}

func signalOwned(owner processOwner) (bool, error) {
	alive, err := sameProcess(owner)
	if err != nil || !alive {
		return false, err
	}
	group, err := syscall.Getpgid(owner.PID)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if group != owner.PID {
		return false, errors.New("agent process group identity changed")
	}
	err = syscall.Kill(-group, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return true, err
}
func supervisorExit(cmd *exec.Cmd, nonce string, waitErr error) error {
	b, err := os.ReadFile(filepath.Join(cmd.Dir, ".agent-exit"))
	if err != nil {
		return fmt.Errorf("supervisor exit status unavailable: %w", errors.Join(waitErr, err))
	}
	parts := strings.Fields(string(b))
	if len(parts) != 2 || parts[0] != nonce {
		return errors.New("invalid supervisor exit identity")
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil || code < 0 || code > 255 {
		return errors.New("invalid supervisor exit status")
	}
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		return fmt.Errorf("unexpected supervisor exit: %v", waitErr)
	}
	if code != 0 {
		return fmt.Errorf("agent exit status %d", code)
	}
	return nil
}

// RecoverScratch is called only for a journaled attempt while its daemon state
// lock is held. No bare PID may authorize a signal.
func RecoverScratch(dir string) error {
	owner, err := readProcess(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		killed, err := signalOwned(owner)
		if err != nil {
			return err
		}
		if killed {
			deadline := time.Now().Add(3 * time.Second)
			for {
				alive, e := sameProcess(owner)
				if e != nil {
					return fmt.Errorf("confirm agent exit: %w", e)
				}
				if !alive {
					break
				}
				if time.Now().After(deadline) {
					return errors.New("owned agent did not exit")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	var failures []error
	for _, name := range []string{".cli-home/.credentials.json", ".cli-home/auth.json", ".agent-lock"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
