package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
)

func TestScopeSharedHomeDaemonHelper(t *testing.T) {
	raw := os.Getenv("LYK_SCOPE_DAEMON_TEST")
	if raw == "" {
		return
	}
	var cfg Config
	must(t, json.Unmarshal([]byte(raw), &cfg))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	cancel()
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("helper daemon failed to stop")
	}
}

func scopeDaemonProcess(t *testing.T, cfg Config) func() {
	t.Helper()
	stop, _ := scopeDaemonProcessControl(t, cfg)
	return stop
}

func scopeDaemonProcessControl(t *testing.T, cfg Config) (func(), func()) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"Server": cfg.Server, "Token": cfg.Token, "StateDir": cfg.StateDir, "Home": cfg.Home, "Device": cfg.Device, "Harnesses": cfg.Harnesses, "Slots": cfg.Slots, "Poll": cfg.Poll, "Heartbeat": cfg.Heartbeat, "Flush": cfg.Flush})
	must(t, err)
	cmd := exec.Command(os.Args[0], "-test.run=^TestScopeSharedHomeDaemonHelper$")
	cmd.Env = append(os.Environ(), "LYK_SCOPE_DAEMON_TEST="+string(raw))
	input, err := cmd.StdinPipe()
	must(t, err)
	log, err := os.Create(filepath.Join(cfg.StateDir, "helper.log"))
	must(t, err)
	cmd.Stdout = log
	cmd.Stderr = log
	must(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			input.Write([]byte("stop\n"))
			input.Close()
			select {
			case err := <-done:
				if err != nil {
					b, _ := os.ReadFile(log.Name())
					t.Errorf("helper: %v %s", err, b)
				}
			case <-time.After(10 * time.Second):
				cmd.Process.Kill()
				<-done
				t.Error("helper stop timed out")
			}
			log.Close()
		})
	}
	kill := func() {
		once.Do(func() {
			must(t, cmd.Process.Kill())
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("killed helper did not exit")
			}
			input.Close()
			log.Close()
		})
	}
	t.Cleanup(stop)
	return stop, kill
}

func TestAttemptReservationProtectsAllMutations(t *testing.T) {
	f := fixture(t)

	control := filepath.Join(f.cfg.Home, ".caches", "control")
	must(t, os.MkdirAll(control, 0700))
	ready, release, launches := filepath.Join(control, "ready"), filepath.Join(control, "release"), filepath.Join(control, "launches")
	bin := filepath.Join(control, "claude")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo claude-test; exit 0; fi\nIFS= read -r first || exit 46\ncase \"$first\" in *'\"subtype\":\"initialize\"'*) echo '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"models-init\",\"response\":{\"models\":[]}}}'; exit 0;; esac\necho launched >> " + strconv.Quote(launches) + "\necho active-data > \"$PWD/active-data.txt\"\necho \"$PWD\" > " + strconv.Quote(ready) + "\nwhile [ ! -f " + strconv.Quote(release) + " ]; do sleep 0.05; done\necho '{\"type\":\"result\",\"result\":\"done\",\"session_id\":\"s\"}'\n"
	must(t, os.WriteFile(bin, []byte(script), 0700))
	f.cfg.Harnesses = map[string]string{"claude": bin}
	f.cfg.Slots = 1
	_, err := f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='claude' WHERE id=$1", f.id)
	must(t, err)
	first := f.id
	name := scratchName(f.id, 1)
	dir := filepath.Join(f.cfg.Home, name)
	old := time.Now().Add(-48 * time.Hour)
	stopFirst := scopeDaemonProcess(t, f.cfg)
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
	for _, p := range []string{filepath.Join(dir, ".finished"), finishedRecord(f.cfg.StateDir, dir)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Error("finished marker present during an active run", p, err)
		}
	}
	actual, err := os.ReadFile(ready)
	must(t, err)
	resolved, err := filepath.EvalSymlinks(dir)
	must(t, err)
	if strings.TrimSpace(string(actual)) != resolved {
		t.Fatalf("CLI scratch %q, expected %q", actual, resolved)
	}
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("active skill"), 0600))
	var process struct {
		PID int `json:"pid"`
	}
	b, err := os.ReadFile(filepath.Join(dir, ".agent-process"))
	must(t, err)
	must(t, json.Unmarshal(b, &process))
	state := t.TempDir()
	other := &daemon{cfg: Config{StateDir: state, Home: f.cfg.Home}, active: map[string]bool{}}
	// A stale marker must not permit another process's GC to mutate a held folder.
	finishedFor(t, state, dir, `{"retention_days":1}`, time.Since(old))
	if err := other.gc(t.Context()); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("GC deleted another daemon's active attempt", err)
	}
	must(t, os.Remove(finishedRecord(state, dir)))
	// A stale journal cannot authorize recovery to kill the current holder's CLI.
	r := reportFixture()
	r.AgentRunID = first
	r.WorkspaceID = f.ws
	r.Scratch = name
	r.RetentionDays = 1
	must(t, other.journal().Put(r))
	other.messages = &Outbox{Dir: filepath.Join(state, "messages"), Send: func(context.Context, Report) error { return nil }}
	other.out = &Outbox{Dir: filepath.Join(state, "outbox"), Send: func(context.Context, Report) error { return nil }}
	if err := other.recoverAttempts(t.Context()); err == nil || !strings.Contains(err.Error(), "scratch is already in use") {
		t.Errorf("recovery ignored other daemon's lock: %v", err)
	}
	if err := syscall.Kill(process.PID, 0); err != nil {
		t.Error("recovery killed active CLI", err)
	}
	if _, err := os.Stat(other.journal().file(r)); err != nil {
		t.Error("conflicted recovery lost journal", err)
	}
	if t.Failed() {
		return
	}
	must(t, os.WriteFile(release, nil, 0600))
	waitFor(t, func() bool {
		var status string
		must(t, f.env.Owner.QueryRow(t.Context(), "SELECT status FROM agent_run WHERE id=$1", first).Scan(&status))
		return status == "completed"
	})
	stopFirst()

}

func TestGCFinishesInterruptedScratchRetirement(t *testing.T) {
	home := t.TempDir()
	d := &daemon{cfg: Config{Home: home}, active: map[string]bool{}}
	removed, held := filepath.Join(home, agent.RetiredScratchPrefix+"interrupted"), filepath.Join(home, agent.RetiredScratchPrefix+"held")
	for _, dir := range []string{removed, held} {
		must(t, os.MkdirAll(filepath.Join(dir, ".cli-home"), 0700))
		must(t, os.WriteFile(filepath.Join(dir, ".cli-home", "auth.json"), []byte("synthetic"), 0600))
	}
	reservation, err := agent.ReserveScratch(held)
	must(t, err)
	defer reservation.Release()
	must(t, d.gc(t.Context()))
	if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted retirement retained secrets", err)
	}
	if _, err := os.Stat(filepath.Join(held, ".cli-home", "auth.json")); err != nil {
		t.Fatal("GC entered held retirement", err)
	}
	reservation.Release()
	must(t, d.gc(t.Context()))
	if _, err := os.Stat(held); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("released retirement kept", err)
	}
}

func TestReusedScopeLosesOldFinishedClockAfterParentExit(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	dir := filepath.Join(home, "scope-crashed")
	must(t, os.Mkdir(dir, 0700))
	marker := finishedRecord(state, dir)
	finishedFor(t, state, dir, `{"retention_days":1}`, 48*time.Hour)
	d := &daemon{cfg: Config{Home: home, StateDir: state}, active: map[string]bool{}}
	reservation, err := agent.ReserveScratch(dir)
	must(t, err)

	must(t, d.unmarkFinished(dir))
	must(t, os.Mkdir(filepath.Join(dir, ".cli-home"), 0700))
	must(t, os.WriteFile(filepath.Join(dir, ".cli-home", "auth.json"), []byte("synthetic"), 0600))
	// Simulate the parent disappearing: the kernel reservation is gone, but the
	// unfinished execution must not inherit the previous run's deletion clock.
	reservation.Release()
	must(t, d.gc(t.Context()))
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("previous finished marker survived", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cli-home", "auth.json")); err != nil {
		t.Fatal("GC collected unfinished crash state", err)
	}
}

func TestOrphanAttemptReconciledBeforeDeletion(t *testing.T) {
	for _, action := range []string{"gc"} {
		t.Run(action, func(t *testing.T) {
			f := fixture(t)
			control := filepath.Join(f.cfg.Home, ".caches", "control")
			must(t, os.MkdirAll(control, 0700))
			ready, again := filepath.Join(control, "ready"), filepath.Join(control, "again")
			violation := filepath.Join(control, "overlap")
			bin := filepath.Join(control, "claude")
			body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo claude-test; exit 0; fi\nIFS= read -r first || exit 46\ncase \"$first\" in *'\"subtype\":\"initialize\"'*) echo '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"models-init\",\"response\":{\"models\":[]}}}'; exit 0;; esac\nif [ -f " + strconv.Quote(ready) + " ]; then if kill -0 \"$(cat " + strconv.Quote(ready) + ")\" 2>/dev/null; then echo overlap > " + strconv.Quote(violation) + "; fi; echo relaunched > " + strconv.Quote(again) + "; echo '{\"type\":\"result\",\"result\":\"done\"}'; exit 0; fi\necho $$ > " + strconv.Quote(ready) + "\nwhile :; do sleep 0.05; done\n"
			must(t, os.WriteFile(bin, []byte(body), 0700))
			f.cfg.Harnesses = map[string]string{"claude": bin}
			f.cfg.Slots = 1
			_, err := f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='claude' WHERE id=$1", f.id)
			must(t, err)
			dir := filepath.Join(f.cfg.Home, scratchName(f.id, 1))
			_, kill := scopeDaemonProcessControl(t, f.cfg)
			waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
			var owner struct {
				PID int `json:"pid"`
			}
			b, err := os.ReadFile(filepath.Join(dir, ".agent-process"))
			must(t, err)
			must(t, json.Unmarshal(b, &owner))
			pidBytes, err := os.ReadFile(ready)
			must(t, err)
			cli, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			must(t, err)
			cli, err = testutil.OwnedFixtureHostPID(owner.PID, cli)
			must(t, err)
			// Recover only the journalled birth/group identity. Once gc removes the
			// journal this is a no-op, including if the numeric PID was reused.
			t.Cleanup(func() {
				if err := agent.RecoverScratch(dir); err != nil {
					t.Errorf("recover fixture: %v", err)
				}
			})
			kill()
			must(t, syscall.Kill(cli, 0))
			must(t, syscall.Kill(owner.PID, 0))
			other := &daemon{cfg: Config{StateDir: t.TempDir(), Home: f.cfg.Home}, active: map[string]bool{}}
			switch action {
			case "gc":
				finishedFor(t, other.cfg.StateDir, dir, `{"retention_days":1}`, 48*time.Hour)
				must(t, other.gc(t.Context()))
			}
			waitFor(t, func() bool {
				return errors.Is(syscall.Kill(owner.PID, 0), syscall.ESRCH) && errors.Is(syscall.Kill(cli, 0), syscall.ESRCH)
			})
			if action != "reuse" {
				if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("scope not deleted", err)
				}
			}
		})
	}
}

// A run's code may write its run folder, so the finished marker there is not
// trusted: gc goes by the copy the daemon keeps in its state folder. Scope
// folders are kept for a while, so a forged old marker would matter there.
func TestGCTrustsOnlyTheFinishedRecordsItKeeps(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &daemon{cfg: Config{Home: home, StateDir: state}, active: map[string]bool{}}
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"scope-planted", "scope-recorded", "scope-forged"} {
		dir := filepath.Join(home, name)
		must(t, os.Mkdir(dir, 0700))
		if name != "scope-planted" {
			must(t, d.markFinished(dir, map[string]int{"retention_days": 1}))
		}
		if name == "scope-recorded" {
			must(t, os.Chtimes(finishedRecord(state, dir), old, old))
		}
		if name != "scope-recorded" {
			must(t, os.WriteFile(filepath.Join(dir, ".finished"), []byte(`{"retention_days":1}`), 0600))
			must(t, os.Chtimes(filepath.Join(dir, ".finished"), old, old))
		}
	}
	must(t, d.gc(t.Context()))
	for name, kept := range map[string]bool{"scope-planted": true, "scope-forged": true, "scope-recorded": false} {
		if _, err := os.Stat(filepath.Join(home, name)); (err == nil) != kept {
			t.Errorf("%s: kept %v, want %v", name, err == nil, kept)
		}
	}
	if _, err := os.Stat(finishedRecord(state, filepath.Join(home, "scope-recorded"))); err == nil {
		t.Error("the record outlived its folder")
	}
}
