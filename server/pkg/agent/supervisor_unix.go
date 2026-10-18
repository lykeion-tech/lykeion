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
// its child's status. The host stops descendants before retiring this witness.
const supervisorScript = `nonce=$1
marker=$2
shift 2
cleanup() { /bin/rm -f -- "$PWD/.cli-home/.credentials.json" "$PWD/.cli-home/auth.json" "$PWD/.cli-home/cli-config.json" "$PWD/.cli-home/.gemini/oauth_creds.json"; }
if ! IFS= read -r permit <&3; then cleanup; exit 125; fi
exec 3<&-
"$@" <&0 &
child=$!
wait "$child"
result=$?
cleanup || result=125
(umask 077; printf '%s %s\n' "$nonce" "$result" > "$marker.tmp") && /bin/mv -f -- "$marker.tmp" "$marker" || exit 125
kill -STOP $$
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

var errProcessObservationIncomplete = errors.New("process group observation incomplete")

const processExitTimeout = 3 * time.Second
const processPollInterval = 10 * time.Millisecond

type groupMember struct {
	owner   processOwner
	stopped bool
	exited  bool
}

type groupOperations struct {
	same    func(processOwner) (bool, error)
	group   func(int) (int, error)
	signal  func(int, syscall.Signal) error
	members func(int) ([]groupMember, error)
}

func signalOwned(owner processOwner) (bool, error) {
	return signalOwnedWith(owner, groupOperations{sameProcess, syscall.Getpgid, syscall.Kill, processGroupMembers})
}

func ownedGroupAlive(owner processOwner, ops groupOperations) (bool, error) {
	alive, err := ops.same(owner)
	if err != nil || !alive {
		return false, err
	}
	group, err := ops.group(owner.PID)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if group != owner.PID {
		return false, errors.New("agent process group identity changed")
	}
	return true, nil
}

func signalOwnedWith(owner processOwner, ops groupOperations) (bool, error) {
	deadline := time.Now().Add(processExitTimeout)
	for {
		alive, err := ownedGroupAlive(owner, ops)
		if err != nil || !alive {
			return false, err
		}
		// Group signalling snapshots membership. Stop every member and observe
		// acknowledgment before killing; otherwise a concurrent fork can escape.
		if err := ops.signal(-owner.PID, syscall.SIGSTOP); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return false, nil
			}
			return false, err
		}
		members, err := ops.members(owner.PID)
		if errors.Is(err, errProcessObservationIncomplete) {
			members, err = nil, nil
		}
		if err != nil {
			return false, err
		}
		stopped, descendants := true, false
		acknowledged := make(map[int]string, len(members))
		for _, member := range members {
			stopped = stopped && (member.stopped || member.exited)
			acknowledged[member.owner.PID] = member.owner.Started
		}
		stopped = stopped && acknowledged[owner.PID] == owner.Started
		if stopped {
			// Enumeration can collect PIDs before reading their stopped states.
			// Reconcile after acknowledgments: every live identity in this later
			// scan must already have been stopped in the earlier scan. A new
			// identity restarts the protocol even if it now appears stopped.
			members, err = ops.members(owner.PID)
			if errors.Is(err, errProcessObservationIncomplete) {
				members, err = nil, nil
			}
			if err != nil {
				return false, err
			}
			witness := false
			for _, member := range members {
				stopped = stopped && (member.stopped || member.exited) && acknowledged[member.owner.PID] == member.owner.Started
				witness = witness || (member.owner.PID == owner.PID && member.owner.Started == owner.Started && member.stopped && !member.exited)
				descendants = descendants || (member.owner.PID != owner.PID && !member.exited)
			}
			stopped = stopped && witness
		}
		if stopped {
			// Keep the supervisor alive while retiring descendants. Losing the group
			// leader first can resume stopped members through orphan-group signals.
			for _, member := range members {
				if member.owner.PID == owner.PID || member.exited {
					continue
				}
				alive, err := ownedGroupAlive(owner, ops)
				if err != nil || !alive {
					return false, err
				}
				same, err := ops.same(member.owner)
				if err != nil {
					return false, err
				}
				if !same {
					continue
				}
				group, err := ops.group(member.owner.PID)
				if errors.Is(err, syscall.ESRCH) {
					continue
				}
				if err != nil {
					return false, err
				}
				if group != owner.PID {
					return false, errors.New("agent descendant left owned process group")
				}
				alive, err = ownedGroupAlive(owner, ops)
				if err != nil || !alive {
					return false, err
				}
				if err := ops.signal(member.owner.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					return false, err
				}
			}
			if !descendants {
				alive, err := ownedGroupAlive(owner, ops)
				if err != nil || !alive {
					return false, err
				}
				err = ops.signal(-owner.PID, syscall.SIGKILL)
				if errors.Is(err, syscall.ESRCH) {
					return false, nil
				}
				return true, err
			}
		}
		if time.Now().After(deadline) {
			return false, errors.New("owned agent group did not stop")
		}
		time.Sleep(processPollInterval)
	}
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

// RecoverScratch reconciles a journaled attempt after its daemon reserves the
// scratch. No bare PID may authorize a signal.
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
			deadline := time.Now().Add(processExitTimeout)
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
				time.Sleep(processPollInterval)
			}
		}
	}
	var failures []error
	for _, name := range []string{".cli-home/.credentials.json", ".cli-home/auth.json", ".cli-home/cli-config.json", ".cli-home/.gemini/oauth_creds.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
