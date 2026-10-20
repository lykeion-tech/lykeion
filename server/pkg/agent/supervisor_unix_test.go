//go:build unix

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"lykeion/server/internal/processidentity"
)

func TestSupervisorKillsLingeringDescendantAfterSuccess(t *testing.T) {
	escaped := filepath.Join(t.TempDir(), "escaped")
	bin := script(t, `(sleep 0.5; echo leaked > '`+escaped+`') &
echo '{"type":"result","is_error":false}'`)
	sess, err := (Claude{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, res := collect(t, sess)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatal("descendant survived completed CLI")
	}
}
func TestSupervisorStatusCannotOverrideProtocolOrExitFailure(t *testing.T) {
	for _, body := range []string{`echo '{"type":"result","is_error":false}';exit 7`, `exit 0`, `echo '{"type":"result","is_error":true,"result":"provider refused"}'`} {
		sess, err := (Claude{Bin: script(t, body)}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		_, res := collect(t, sess)
		if res.Err == nil {
			t.Fatalf("status accepted failure: %s", body)
		}
	}
}
func TestSupervisorMissingAndStaleExitMarkersFailClosed(t *testing.T) {
	for _, body := range []string{`kill -KILL "$PPID"; exit 0`, `printf 'old-nonce 0\n' > "$PWD/.agent-exit";kill -KILL "$PPID";exit 0`} {
		dir := t.TempDir()
		sess, err := (Claude{Bin: script(t, body)}).Execute(t.Context(), "p", ExecOptions{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		_, res := collect(t, sess)
		if res.Err == nil {
			t.Fatal("missing/stale marker accepted")
		}
	}
}
func TestRecoveryRefusesReusedProcessIdentity(t *testing.T) {
	child := exec.Command("sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	birth, err := processidentity.Start(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, _ := json.Marshal(processOwner{PID: child.Process.Pid, Started: birth + "-stale", Nonce: "owned-attempt"})
	if err := os.WriteFile(filepath.Join(dir, ".agent-process"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverScratch(dir); err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated reused PID signalled: %v", err)
	}
}
func TestLaunchGatePreventsUnjournaledCLI(t *testing.T) {
	dir := t.TempDir()
	launched := filepath.Join(dir, "launched")
	original := exec.Command("/bin/sh", "-c", "echo launched > '"+launched+"'")
	original.Dir = dir
	cmd, read, write, _, err := supervised(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if err := configureProcess(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	write.Close()
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 125 {
		t.Fatalf("gate EOF status: %v", err)
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatal("CLI ran before ownership gate")
	}
}
func TestRetentionLimitPreservesCurrentRecord(t *testing.T) {
	dir := t.TempDir()
	r := &messageRetention{dir: dir}
	defer r.close()
	if err := r.put(Message{Type: "log", Content: []byte(`"first"`)}); err != nil {
		t.Fatal(err)
	}
	r.size = retainedMessageBytes
	err := r.put(Message{Type: "log", Content: []byte(`"overflow evidence"`)})
	if err == nil {
		t.Fatal("missing explicit cap failure")
	}
	b, err := os.ReadFile(r.paths[1])
	if err != nil || !strings.Contains(string(b), "overflow evidence") {
		t.Fatalf("overflow record lost: %s %v", b, err)
	}
}

func TestRecoveryRefusesLiveProcessWithoutOwnedGroup(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	group, err := syscall.Getpgid(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if group == child.Process.Pid {
		t.Fatal("fixture unexpectedly owns a group")
	}
	birth, err := processidentity.Start(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, _ := json.Marshal(processOwner{PID: child.Process.Pid, Started: birth, Nonce: "owned-attempt"})
	if err := os.WriteFile(filepath.Join(dir, ".agent-process"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverScratch(dir); err == nil {
		t.Fatal("unowned group accepted")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unrelated child was signalled", err)
	}
}

func TestSupervisorPreservesExecutableResolution(t *testing.T) {
	t.Run("relative to command directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "harness"), []byte("#!/bin/sh\necho '{\"type\":\"result\",\"is_error\":false,\"session_id\":\"relative\"}'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		sess, err := (Claude{Bin: "./harness"}).Execute(t.Context(), "p", ExecOptions{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		_, result := collect(t, sess)
		if result.Err != nil || result.SessionID != "relative" {
			t.Fatalf("relative executable: %+v", result)
		}
	})
	t.Run("parent PATH remains authoritative", func(t *testing.T) {
		parent, child := t.TempDir(), t.TempDir()
		for dir, session := range map[string]string{parent: "parent", child: "child"} {
			body := "#!/bin/sh\necho '{\"type\":\"result\",\"is_error\":false,\"session_id\":\"" + session + "\"}'\n"
			if err := os.WriteFile(filepath.Join(dir, "harness"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", parent)
		sess, err := (Claude{Bin: "harness"}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir(), Env: []string{"PATH=" + child}})
		if err != nil {
			t.Fatal(err)
		}
		_, result := collect(t, sess)
		if result.Err != nil || result.SessionID != "parent" {
			t.Fatalf("PATH resolution: %+v", result)
		}
	})
}

func TestRecoveryRemovesEveryHarnessSigninCopy(t *testing.T) {
	for harness, file := range map[string]string{"claude": ".credentials.json", "codex": "auth.json", "cursor": "cli-config.json", "antigravity": ".gemini/oauth_creds.json"} {
		t.Run(harness, func(t *testing.T) {
			dir := t.TempDir()
			copy := filepath.Join(dir, ".cli-home", file)
			if err := os.MkdirAll(filepath.Dir(copy), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(copy, []byte("synthetic signin"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := RecoverScratch(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(copy); !os.IsNotExist(err) {
				t.Fatalf("recovery left %s copied sign-in: %v", harness, err)
			}
		})
	}
}

// A group signal can snapshot membership before a running member forks.
// The kernel model preserves that boundary while executing the real shutdown.
func TestOwnedShutdownStopsLateForkBeforeKillingWitness(t *testing.T) {
	const leader, parent, late = 101, 102, 103
	members := map[int]bool{leader: false, parent: false}
	forked := false
	ops := groupOperations{
		same:  func(owner processOwner) (bool, error) { _, live := members[owner.PID]; return live, nil },
		group: func(int) (int, error) { return leader, nil },
		members: func(int) ([]groupMember, error) {
			var snapshot []groupMember
			for pid, stopped := range members {
				snapshot = append(snapshot, groupMember{owner: processOwner{PID: pid, Started: "birth"}, stopped: stopped})
			}
			return snapshot, nil
		},
		signal: func(target int, signal syscall.Signal) error {
			var snapshot []int
			if target == -leader {
				for pid := range members {
					snapshot = append(snapshot, pid)
				}
			} else {
				snapshot = []int{target}
			}
			if !forked && !members[parent] {
				members[late] = false
				forked = true
			}
			for _, pid := range snapshot {
				switch signal {
				case syscall.SIGSTOP:
					members[pid] = true
				case syscall.SIGKILL:
					if pid == leader && len(members) > 1 {
						t.Error("ownership witness killed before descendants")
					}
					delete(members, pid)
				default:
					t.Fatalf("unexpected signal: %v", signal)
				}
			}
			return nil
		},
	}
	killed, err := signalOwnedWith(processOwner{PID: leader, Started: "birth", Nonce: "attempt"}, ops)
	if err != nil || !killed {
		t.Fatalf("shutdown: killed=%v err=%v", killed, err)
	}
	if len(members) != 0 {
		t.Fatalf("owned descendants survived shutdown after witness exit: %v", members)
	}
}

func TestSupervisorKeepsOwnershipUntilHostFinishesShutdown(t *testing.T) {
	dir := t.TempDir()
	original := exec.Command("/bin/sh", "-c", "exit 0")
	original.Dir = dir
	cmd, read, write, nonce, err := supervised(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	if err := configureProcess(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := recordProcess(cmd, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := write.Write([]byte("run\n")); err != nil {
		t.Fatal(err)
	}
	write.Close()
	waitFile(t, filepath.Join(dir, ".agent-exit"))
	owner, err := readProcess(dir)
	if err != nil {
		t.Fatal(err)
	}
	alive, err := sameProcess(owner)
	if err != nil || !alive {
		t.Fatalf("supervisor discarded ownership before host confirmed group shutdown: alive=%v err=%v", alive, err)
	}
	group, err := syscall.Getpgid(owner.PID)
	if err != nil || group != owner.PID {
		t.Fatalf("ownership witness left its group: group=%d err=%v", group, err)
	}
}

func TestOwnedShutdownFailsClosedWhenProofChanges(t *testing.T) {
	for _, scenario := range []string{"missing supervisor", "reused supervisor", "changed supervisor group", "lost supervisor after stop", "enumeration error", "changed descendant group"} {
		t.Run(scenario, func(t *testing.T) {
			owner := processOwner{PID: 101, Started: "birth", Nonce: "attempt"}
			stopped := false
			signals := 0
			want := errors.New("cannot enumerate")
			ops := groupOperations{
				same: func(processOwner) (bool, error) {
					return scenario != "missing supervisor" && scenario != "reused supervisor" && !(stopped && scenario == "lost supervisor after stop"), nil
				},
				group: func(pid int) (int, error) {
					if scenario == "changed supervisor group" || (pid == 102 && scenario == "changed descendant group") {
						return 99, nil
					}
					return 101, nil
				},
				members: func(int) ([]groupMember, error) {
					if scenario == "enumeration error" {
						return nil, want
					}
					return []groupMember{{owner: owner, stopped: true}, {owner: processOwner{PID: 102, Started: "child-birth"}, stopped: true}}, nil
				},
				signal: func(_ int, signal syscall.Signal) error {
					signals++
					if signal != syscall.SIGSTOP {
						t.Fatalf("destructive signal after proof changed: %v", signal)
					}
					stopped = true
					return nil
				},
			}
			killed, err := signalOwnedWith(owner, ops)
			if killed {
				t.Fatal("reported a kill after proof changed")
			}
			if scenario == "enumeration error" && !errors.Is(err, want) {
				t.Fatalf("enumeration error hidden: %v", err)
			}
			if strings.Contains(scenario, "group") && err == nil {
				t.Fatal("changed group was accepted")
			}
			if signals > 1 {
				t.Fatalf("retried signal after proof changed: %d", signals)
			}
		})
	}
}

func TestOwnedShutdownDoesNotSignalZombieDescendant(t *testing.T) {
	owner := processOwner{PID: 101, Started: "birth", Nonce: "attempt"}
	ops := groupOperations{
		same: func(p processOwner) (bool, error) {
			if p.PID != owner.PID {
				t.Fatal("zombie treated as live")
			}
			return true, nil
		},
		group: func(int) (int, error) { return owner.PID, nil },
		members: func(int) ([]groupMember, error) {
			return []groupMember{{owner: owner, stopped: true}, {owner: processOwner{PID: 102}, exited: true}}, nil
		},
		signal: func(pid int, _ syscall.Signal) error {
			if pid != -owner.PID {
				t.Fatal("individual zombie signal")
			}
			return nil
		},
	}
	if killed, err := signalOwnedWith(owner, ops); !killed || err != nil {
		t.Fatalf("zombie blocked shutdown: %v %v", killed, err)
	}
}

func TestRecoveryReconcilesStoppedCompletedSupervisor(t *testing.T) {
	dir := t.TempDir()
	original := exec.Command("/bin/sh", "-c", "exit 0")
	original.Dir = dir
	cmd, read, write, nonce, err := supervised(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	if err := configureProcess(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	if err := recordProcess(cmd, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := write.Write([]byte("run\n")); err != nil {
		t.Fatal(err)
	}
	write.Close()
	waitFile(t, filepath.Join(dir, ".agent-exit"))
	if err := RecoverScratch(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err := supervisorExit(cmd, nonce, err); err != nil {
			t.Fatal(err)
		}
	case <-time.After(processExitTimeout):
		t.Fatal("recovery left stopped supervisor")
	}
}

func TestCancellationDoesNotBypassRejectedOwnership(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := script(t, `echo $$ > '`+ready+`'
exec sleep 30`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sess, err := (Claude{Bin: bin}).Execute(ctx, "p", ExecOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, ready)
	owner, err := readProcess(dir)
	if err != nil {
		t.Fatal(err)
	}
	pidText, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil {
		t.Fatal(err)
	}
	birth, err := processidentity.Start(pid)
	if err != nil {
		t.Fatal(err)
	}
	child := processOwner{PID: pid, Started: birth}
	t.Cleanup(func() {
		b, _ := json.Marshal(owner)
		_ = os.WriteFile(filepath.Join(dir, ".agent-process"), b, 0600)
		_, _ = signalOwned(owner)
		// The RED path loses its supervisor; clean only the separately recorded
		// fixture identity, still in this test's original group.
		if alive, _ := sameProcess(child); alive {
			if group, _ := syscall.Getpgid(child.PID); group == owner.PID {
				_ = syscall.Kill(child.PID, syscall.SIGKILL)
			}
		}
		select {
		case <-sess.Done:
		case <-time.After(processExitTimeout):
			t.Error("fixture did not finish after verified cleanup")
		}
	})
	if err := os.WriteFile(filepath.Join(dir, ".agent-process"), []byte("invalid ownership"), 0600); err != nil {
		t.Fatal(err)
	}
	cancel()
	// Cover the previous os/exec one-second fallback kill, independently of
	// the rejected ownership proof. The assertion is liveness, not timing.
	time.Sleep(1200 * time.Millisecond)
	alive, err := sameProcess(owner)
	group, groupErr := syscall.Getpgid(owner.PID)
	if err != nil || !alive || groupErr != nil || group != owner.PID {
		t.Fatalf("cancellation bypassed rejected ownership: alive=%v err=%v group=%d groupErr=%v", alive, err, group, groupErr)
	}
}

func TestOwnedShutdownDoesNotSignalReusedDescendant(t *testing.T) {
	owner := processOwner{PID: 101, Started: "birth", Nonce: "attempt"}
	checked := false
	ops := groupOperations{
		same: func(p processOwner) (bool, error) {
			if p.PID == 102 {
				checked = true
				return false, nil
			}
			return true, nil
		},
		group: func(int) (int, error) { return owner.PID, nil },
		members: func(int) ([]groupMember, error) {
			members := []groupMember{{owner: owner, stopped: true}}
			if !checked {
				members = append(members, groupMember{owner: processOwner{PID: 102, Started: "old-child"}, stopped: true})
			}
			return members, nil
		},
		signal: func(pid int, _ syscall.Signal) error {
			if pid == 102 {
				t.Fatal("signalled reused descendant PID")
			}
			return nil
		},
	}
	if killed, err := signalOwnedWith(owner, ops); !killed || err != nil {
		t.Fatalf("shutdown: %v %v", killed, err)
	}
	if !checked {
		t.Fatal("descendant birth was not checked")
	}
}

func TestSupervisorMonitorIgnoresStaleCompletionMarker(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sess, err := (Claude{Bin: script(t, `echo ready > '`+ready+`'
exec sleep 30`)}).Execute(ctx, "p", ExecOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); collect(t, sess) }()
	waitFile(t, ready)
	owner, err := readProcess(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-exit"), []byte("stale-attempt 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * processPollInterval)
	alive, err := sameProcess(owner)
	group, groupErr := syscall.Getpgid(owner.PID)
	if err != nil || !alive || groupErr != nil || group != owner.PID {
		t.Fatalf("stale marker retired current supervisor: %v %v %v", alive, err, groupErr)
	}
}

// Membership enumeration and state observation need not be one atomic snapshot.
// A parent can finish forking after its PID was collected but before its stop
// acknowledgment is read, leaving the new child absent from that scan.
func TestOwnedShutdownReconcilesMembershipAfterStopAcknowledgments(t *testing.T) {
	for _, forkAgain := range []bool{false, true} {
		t.Run(map[bool]string{false: "late child", true: "late child forks during rescan"}[forkAgain], func(t *testing.T) {
			const leader, child, grandchild, survivor = 101, 102, 103, 104
			members := map[int]bool{leader: false}
			scans := 0
			ops := groupOperations{
				same:  func(owner processOwner) (bool, error) { _, alive := members[owner.PID]; return alive, nil },
				group: func(int) (int, error) { return leader, nil },
				members: func(int) ([]groupMember, error) {
					scans++
					var pids []int
					for pid := range members {
						pids = append(pids, pid)
					}
					if scans == 1 {
						members[child] = false
						members[leader] = true
					}
					if scans == 2 && forkAgain {
						members[grandchild] = false
						members[child] = true
					}
					var snapshot []groupMember
					for _, pid := range pids {
						snapshot = append(snapshot, groupMember{owner: processOwner{PID: pid, Started: "birth"}, stopped: members[pid]})
					}
					return snapshot, nil
				},
				signal: func(target int, signal syscall.Signal) error {
					var pids []int
					if target < 0 {
						for pid := range members {
							pids = append(pids, pid)
						}
					} else {
						pids = []int{target}
					}
					if signal == syscall.SIGKILL && target == -leader {
						for _, stopped := range members {
							if !stopped {
								members[survivor] = false
								break
							}
						}
					}
					for _, pid := range pids {
						switch signal {
						case syscall.SIGSTOP:
							// The first stop remains pending until the first state observation.
							if scans > 0 {
								members[pid] = true
							}
						case syscall.SIGKILL:
							delete(members, pid)
						default:
							t.Fatalf("unexpected signal: %v", signal)
						}
					}
					return nil
				},
			}
			killed, err := signalOwnedWith(processOwner{PID: leader, Started: "birth", Nonce: "attempt"}, ops)
			if !killed || err != nil {
				t.Fatalf("shutdown: %v %v", killed, err)
			}
			if len(members) != 0 {
				t.Fatalf("unobserved descendants survived witness retirement: %v", members)
			}
		})
	}
}

func TestOwnedShutdownRetriesVanishedParentObservation(t *testing.T) {
	const leader, grandchild = 101, 103
	owner := processOwner{PID: leader, Started: "birth", Nonce: "attempt"}
	members := map[int]bool{leader: true}
	scans := 0
	ops := groupOperations{
		same:  func(p processOwner) (bool, error) { _, alive := members[p.PID]; return alive, nil },
		group: func(int) (int, error) { return leader, nil },
		members: func(int) ([]groupMember, error) {
			scans++
			if scans == 2 {
				// This capture included an unacknowledged parent. It forked after PID
				// capture, then vanished before its state could be read.
				members[grandchild] = false
				return nil, errProcessObservationIncomplete
			}
			var snapshot []groupMember
			for pid, stopped := range members {
				snapshot = append(snapshot, groupMember{owner: processOwner{PID: pid, Started: "birth"}, stopped: stopped})
			}
			return snapshot, nil
		},
		signal: func(target int, signal syscall.Signal) error {
			if signal == syscall.SIGSTOP {
				for pid := range members {
					members[pid] = true
				}
				return nil
			}
			if target == -leader {
				if len(members) > 1 {
					t.Fatal("retired witness after an incomplete observation")
				}
				delete(members, leader)
			} else {
				delete(members, target)
			}
			return nil
		},
	}
	killed, err := signalOwnedWith(owner, ops)
	if !killed || err != nil {
		t.Fatalf("incomplete observation prevented verified retry: %v %v", killed, err)
	}
	if len(members) != 0 {
		t.Fatalf("late grandchild survived: %v", members)
	}
}

func TestOwnedShutdownReconcilesNewZombieIdentity(t *testing.T) {
	const leader, zombie, grandchild = 101, 102, 103
	owner := processOwner{PID: leader, Started: "birth", Nonce: "attempt"}
	live := map[int]bool{leader: true}
	scans := 0
	ops := groupOperations{
		same:  func(p processOwner) (bool, error) { _, alive := live[p.PID]; return alive, nil },
		group: func(int) (int, error) { return leader, nil },
		members: func(int) ([]groupMember, error) {
			scans++
			if scans == 2 {
				live[grandchild] = false
				return []groupMember{{owner: owner, stopped: true}, {owner: processOwner{PID: zombie, Started: "parent-birth"}, exited: true}}, nil
			}
			var members []groupMember
			for pid, stopped := range live {
				members = append(members, groupMember{owner: processOwner{PID: pid, Started: "birth"}, stopped: stopped})
			}
			return members, nil
		},
		signal: func(target int, signal syscall.Signal) error {
			if signal == syscall.SIGSTOP {
				for pid := range live {
					live[pid] = true
				}
				return nil
			}
			if target == -leader {
				if len(live) > 1 {
					t.Fatal("new zombie hid a live grandchild before witness retirement")
				}
				delete(live, leader)
			} else {
				delete(live, target)
			}
			return nil
		},
	}
	if killed, err := signalOwnedWith(owner, ops); !killed || err != nil {
		t.Fatalf("shutdown: %v %v", killed, err)
	}
	if len(live) != 0 {
		t.Fatalf("grandchild survived: %v", live)
	}
}

// The supervisor runs outside the sandbox; a link planted where it writes
// its exit marker never carries that write to another file.
func TestTheExitMarkerIsNotWrittenThroughALink(t *testing.T) {
	for _, planted := range []string{".agent-exit.tmp", ".agent-exit"} {
		thesis := filepath.Join(t.TempDir(), "thesis.tex")
		if err := os.WriteFile(thesis, []byte("THESIS"), 0o600); err != nil {
			t.Fatal(err)
		}
		bin := script(t, `ln -s '`+thesis+`' "$PWD/`+planted+`"
echo '{"type":"result","is_error":false}'`)
		sess, err := (Claude{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		collectWithin(t, sess, 20*time.Second)
		if b, err := os.ReadFile(thesis); err != nil || string(b) != "THESIS" {
			t.Fatalf("%s: the linked file became %q %v", planted, b, err)
		}
	}
}

// An exit marker that is no regular file (a device, a pipe) is never read
// whole or waited on.
func TestAnExitMarkerThatIsNoFileNeverHangs(t *testing.T) {
	for _, plant := range []string{`ln -s /dev/zero "$PWD/.agent-exit"`, `mkfifo "$PWD/.agent-exit"`} {
		bin := script(t, plant+`
sleep 0.3
echo '{"type":"result","is_error":false}'`)
		sess, err := (Claude{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		collectWithin(t, sess, 20*time.Second)
	}
}
