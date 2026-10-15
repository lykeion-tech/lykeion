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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/pkg/agent"
)

func TestScopeJournalUpdateFailureNeverCompletes(t *testing.T) {
	state, home := t.TempDir(), t.TempDir()
	ws, user := uuid.New(), uuid.New()
	c := claimFor(ws, uuid.New(), user, "analysis")
	runtime := uuid.New()
	c.AgentRun.RuntimeID = &runtime
	var launched atomic.Bool
	d := &daemon{cfg: Config{StateDir: state, Home: home, ScopeWait: 5 * time.Second, NewBackend: func(string, string) (agent.Backend, error) {
		launched.Store(true)
		return nil, errors.New("unexpected backend")
	}}}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
	must(t, SavePolicy(state, ws, Policy{Mode: ModePrivate}))
	name := privateScratch(c)
	d.holdScratch(name)
	finished := make(chan error, 1)
	go func() { finished <- d.runAgentRun(t.Context(), c) }()
	original := Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}
	path := d.journal().file(original)
	waitFor(t, func() bool { _, err := os.Stat(path); return err == nil })
	backup := path + ".saved"
	must(t, os.Rename(path, backup))
	must(t, os.Mkdir(path, 0700))
	must(t, os.WriteFile(filepath.Join(path, "block"), nil, 0600))
	d.releaseScratch(name)
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("journal replacement I/O failure was lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed journal update did not settle")
	}
	if launched.Load() {
		t.Fatal("launched before durable scope journal update")
	}
	report, err := readReport(d.out.file(original))
	must(t, err)
	if report.Kind != "fail" || report.FailureReason != agent.ReasonAgentError {
		t.Fatalf("journal error persisted a successful settlement: %+v", report)
	}
	prior, err := readReport(backup)
	must(t, err)
	if prior.Kind != "fail" || prior.Scratch != "" {
		t.Fatalf("initial recovery record changed: %+v", prior)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("scope mutated before journal durability: %v %v", entries, err)
	}
}

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
	raw, err := json.Marshal(map[string]any{"Server": cfg.Server, "Token": cfg.Token, "StateDir": cfg.StateDir, "Home": cfg.Home, "Device": cfg.Device, "Harnesses": cfg.Harnesses, "Slots": cfg.Slots, "Poll": cfg.Poll, "Heartbeat": cfg.Heartbeat, "Flush": cfg.Flush, "ScopeWait": cfg.ScopeWait})
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

func TestSharedScopeReservationProtectsAllMutations(t *testing.T) {
	f := fixture(t)
	must(t, SavePolicy(f.cfg.StateDir, f.ws, Policy{Mode: ModePrivate}))
	control := t.TempDir()
	ready, release, launches := filepath.Join(control, "ready"), filepath.Join(control, "release"), filepath.Join(control, "launches")
	bin := filepath.Join(control, "cursor")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo cursor-test; exit 0; fi\ncat >/dev/null\necho launched >> " + strconv.Quote(launches) + "\necho active-config > \"$PWD/.lyk-config.json\"\necho \"$PWD\" > " + strconv.Quote(ready) + "\nwhile [ ! -f " + strconv.Quote(release) + " ]; do sleep 0.05; done\necho '{\"type\":\"result\",\"result\":\"done\",\"session_id\":\"s\"}'\n"
	must(t, os.WriteFile(bin, []byte(script), 0700))
	f.cfg.Harnesses = map[string]string{"cursor": bin}
	f.cfg.Slots = 1
	_, err := f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='cursor' WHERE id=$1", f.id)
	must(t, err)
	first := f.id
	c := claimFor(f.ws, f.card, f.user, "scoper")
	c.AgentRun.Harness = "cursor"
	name := privateScratch(c)
	dir := filepath.Join(f.cfg.Home, name)
	must(t, os.Mkdir(dir, 0700))
	must(t, os.WriteFile(filepath.Join(dir, ".finished"), []byte(`{"retention_days":1}`), 0600))
	old := time.Now().Add(-48 * time.Hour)
	must(t, os.Chtimes(filepath.Join(dir, ".finished"), old, old))
	stopFirst := scopeDaemonProcess(t, f.cfg)
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
	if _, err := os.Stat(filepath.Join(dir, ".finished")); !errors.Is(err, os.ErrNotExist) {
		t.Error("old finished marker survived beginning a reused run", err)
	}
	actual, err := os.ReadFile(ready)
	must(t, err)
	resolved, err := filepath.EvalSymlinks(dir)
	must(t, err)
	if strings.TrimSpace(string(actual)) != resolved {
		t.Fatalf("CLI scratch %q, expected %q", actual, resolved)
	}
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("active skill"), 0600))
	files := []string{"SKILL.md", ".lyk-config.json", ".cursor/mcp.json"}
	before := map[string]string{}
	for _, file := range files {
		b, err := os.ReadFile(filepath.Join(dir, file))
		must(t, err)
		before[file] = string(b)
	}
	var process struct {
		PID int `json:"pid"`
	}
	b, err := os.ReadFile(filepath.Join(dir, ".agent-process"))
	must(t, err)
	must(t, json.Unmarshal(b, &process))
	state := t.TempDir()
	other := &daemon{cfg: Config{StateDir: state, Home: f.cfg.Home}, active: map[string]bool{}}
	f.enqueue(t)
	_, err = f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='cursor',skill='competing skill' WHERE id=$1", f.id)
	must(t, err)
	cfg := f.cfg
	cfg.StateDir = t.TempDir()
	stopOther := scopeDaemonProcess(t, cfg)
	waitFor(t, func() bool { return f.status(t) == "failed" })
	var failure string
	must(t, f.env.Owner.QueryRow(t.Context(), "SELECT error FROM agent_run WHERE id=$1", f.id).Scan(&failure))
	if !strings.Contains(failure, "scratch is already in use") {
		t.Error("wrong cross-process refusal", failure)
	}
	for _, file := range files {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(b) != before[file] {
			t.Errorf("active %s changed: %q %v", file, b, err)
		}
	}
	b, err = os.ReadFile(launches)
	must(t, err)
	if strings.Count(string(b), "launched") != 1 {
		t.Error("competing daemon launched", string(b))
	}
	// A stale marker must not permit another process's GC or forget to mutate a held folder.
	must(t, os.WriteFile(filepath.Join(dir, ".finished"), []byte(`{"retention_days":1}`), 0600))
	must(t, os.Chtimes(filepath.Join(dir, ".finished"), old, old))
	if err := other.gc(t.Context()); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("GC deleted another daemon's active scope", err)
	}
	if err := other.ForgetScope(f.ws, f.user, "cursor", c.AgentRun.ContinuityScope); err == nil {
		t.Error("forgot another daemon's held scope")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("forget deleted another daemon's active scope", err)
	}
	must(t, os.Remove(filepath.Join(dir, ".finished")))
	// A stale journal cannot authorize recovery to kill the current holder's CLI.
	r := reportFixture()
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
	stopOther()
	must(t, os.WriteFile(release, nil, 0600))
	waitFor(t, func() bool {
		var status string
		must(t, f.env.Owner.QueryRow(t.Context(), "SELECT status FROM agent_run WHERE id=$1", first).Scan(&status))
		return status == "completed"
	})
	stopFirst()
	f.enqueue(t)
	_, err = f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='cursor',skill='next skill' WHERE id=$1", f.id)
	must(t, err)
	stopNext := scopeDaemonProcess(t, cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopNext()
	b, err = os.ReadFile(filepath.Join(dir, "SKILL.md"))
	must(t, err)
	if string(b) != "next skill" {
		t.Fatalf("released scope could not refresh skill: %q", b)
	}
	b, err = os.ReadFile(launches)
	must(t, err)
	if strings.Count(string(b), "launched") != 2 {
		t.Fatalf("launches: %s", b)
	}
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
	home := t.TempDir()
	dir := filepath.Join(home, "scope-crashed")
	must(t, os.Mkdir(dir, 0700))
	marker := filepath.Join(dir, ".finished")
	must(t, os.WriteFile(marker, []byte(`{"retention_days":1}`), 0600))
	past := time.Now().Add(-48 * time.Hour)
	must(t, os.Chtimes(marker, past, past))
	reservation, err := agent.ReserveScratch(dir)
	must(t, err)
	must(t, reuseScratch(dir))
	must(t, os.Mkdir(filepath.Join(dir, ".cli-home"), 0700))
	must(t, os.WriteFile(filepath.Join(dir, ".cli-home", "auth.json"), []byte("synthetic"), 0600))
	// Simulate the parent disappearing: the kernel reservation is gone, but the
	// unfinished execution must not inherit the previous run's deletion clock.
	reservation.Release()
	d := &daemon{cfg: Config{Home: home}, active: map[string]bool{}}
	must(t, d.gc(t.Context()))
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("previous finished marker survived", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cli-home", "auth.json")); err != nil {
		t.Fatal("GC collected unfinished crash state", err)
	}
}

func TestOrphanScopeReconciledBeforeReuseOrDeletion(t *testing.T) {
	for _, action := range []string{"reuse", "forget", "gc"} {
		t.Run(action, func(t *testing.T) {
			f := fixture(t)
			control := t.TempDir()
			ready, again := filepath.Join(control, "ready"), filepath.Join(control, "again")
			violation := filepath.Join(control, "overlap")
			bin := filepath.Join(control, "cursor")
			body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo cursor-test; exit 0; fi\ncat >/dev/null\nif [ -f " + strconv.Quote(ready) + " ]; then if kill -0 \"$(cat " + strconv.Quote(ready) + ")\" 2>/dev/null; then echo overlap > " + strconv.Quote(violation) + "; fi; echo relaunched > " + strconv.Quote(again) + "; echo '{\"type\":\"result\",\"result\":\"done\"}'; exit 0; fi\necho $$ > " + strconv.Quote(ready) + "\nwhile :; do sleep 0.05; done\n"
			must(t, os.WriteFile(bin, []byte(body), 0700))
			f.cfg.Harnesses = map[string]string{"cursor": bin}
			f.cfg.Slots = 1
			_, err := f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='cursor' WHERE id=$1", f.id)
			must(t, err)
			c := claimFor(f.ws, f.card, f.user, "scoper")
			c.AgentRun.Harness = "cursor"
			dir := filepath.Join(f.cfg.Home, privateScratch(c))
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
			t.Cleanup(func() { _ = syscall.Kill(-owner.PID, syscall.SIGKILL) })
			kill()
			must(t, syscall.Kill(cli, 0))
			must(t, syscall.Kill(owner.PID, 0))
			other := &daemon{cfg: Config{StateDir: t.TempDir(), Home: f.cfg.Home}, active: map[string]bool{}}
			switch action {
			case "reuse":
				f.enqueue(t)
				_, err = f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET harness='cursor',skill='next skill' WHERE id=$1", f.id)
				must(t, err)
				cfg := f.cfg
				cfg.StateDir = other.cfg.StateDir
				stop := scopeDaemonProcess(t, cfg)
				waitFor(t, func() bool { return f.status(t) == "completed" })
				stop()
				if _, err := os.Stat(again); err != nil {
					t.Fatal("next CLI did not launch", err)
				}
				if _, err := os.Stat(violation); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("second CLI launched before orphan CLI exit", err)
				}
			case "forget":
				must(t, other.ForgetScope(f.ws, f.user, "cursor", c.AgentRun.ContinuityScope))
			case "gc":
				marker := filepath.Join(dir, ".finished")
				must(t, os.WriteFile(marker, []byte(`{"retention_days":1}`), 0600))
				old := time.Now().Add(-48 * time.Hour)
				must(t, os.Chtimes(marker, old, old))
				must(t, other.gc(t.Context()))
			}
			if err := syscall.Kill(owner.PID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("%s proceeded while orphan supervisor survived: %v", action, err)
			}
			if err := syscall.Kill(cli, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("%s proceeded while orphan CLI survived: %v", action, err)
			}
			if action != "reuse" {
				if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("scope not deleted", err)
				}
			}
		})
	}
}
