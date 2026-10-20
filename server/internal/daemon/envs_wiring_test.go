package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// readyPython is a Bases whose Python base is marked ready and whose venv
// command only lays out the folder; every other base fails to fetch.
func readyPython(t *testing.T) *envs.Bases {
	t.Helper()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "python"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "python", "ready"), nil, 0o644))
	return &envs.Bases{Dir: dir,
		Run: func(_ context.Context, _ []string, _, _ string, args ...string) ([]byte, error) {
			if !slices.Contains(args, "venv") {
				return nil, errors.New("unexpected command")
			}
			bin := filepath.Join(args[len(args)-1], "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(bin, "python"), nil, 0o755)
		},
		Fetch: func(context.Context, string, string, string) error { return errors.New("offline") }}
}

type launched struct {
	dir  string
	env  []string
	opts agent.ExecOptions
}

// captureLaunches records each run's working folder and environment.
func captureLaunches(f *daemonFixture) <-chan launched {
	got := make(chan launched, 8)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			got <- launched{o.Dir, o.Env, o}
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			close(msgs)
			done <- agent.Result{}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	return got
}

func next(t *testing.T, got <-chan launched) launched {
	t.Helper()
	select {
	case l := <-got:
		return l
	case <-time.After(8 * time.Second):
		t.Fatal("backend not started")
		return launched{}
	}
}

func lookup(env []string, key string) (string, bool) {
	v := ""
	ok := false
	for _, e := range env {
		if s, found := strings.CutPrefix(e, key+"="); found {
			v, ok = s, true
		}
	}
	return v, ok
}

func (f *daemonFixture) enqueueFor(t *testing.T, card uuid.UUID) {
	t.Helper()
	must(t, f.runs.Store.InWorkspace(context.Background(), f.ws, f.user, func(tx *store.Tx) error {
		_, err := f.runs.Enqueue(context.Background(), tx, f.user, service.NewAgentRun{Card: card, Column: "grounding", Round: 1, Role: "scoper", Harness: "claude", Prompt: "scope", MaxAttempts: 1})
		return err
	}))
}

func (f *daemonFixture) newCard(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	must(t, f.runs.Store.InWorkspace(context.Background(), f.ws, f.user, func(tx *store.Tx) error {
		c, err := tx.Q.CreateCard(context.Background(), db.CreateCardParams{WorkspaceID: f.ws, Number: 2, Title: "t2", Question: "q2", OwnerID: f.user, FollowsFrom: []uuid.UUID{}})
		id = c.ID
		return err
	}))
	return id
}

func (f *daemonFixture) settled(t *testing.T, n int) func() bool {
	return func() bool {
		var c int
		must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run WHERE workspace_id=$1 AND status IN ('completed','failed')", f.ws).Scan(&c))
		return c >= n
	}
}

// Code runs only through the door: the CLI's own environment, and what its
// shell starts, carry none of the study's interpreters or folders, which the
// study's code may have planted. The study's folder is there for the door.
func TestTheCLIDoesNotGetTheStudyEnvironment(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	got := captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	l := next(t, got)
	waitFor(t, f.settled(t, 1))
	stopDaemon(t, cancel, done)
	study := filepath.Join(f.cfg.EnvDir, "studies", f.ws.String(), f.card.String())
	if fi, err := os.Stat(study); err != nil || !fi.IsDir() {
		t.Fatalf("no study folder: %v", err)
	}
	{
		o := l.opts
		for _, env := range [][]string{l.env, agent.ChildEnv(o)} {
			for _, k := range []string{"VIRTUAL_ENV", "R_LIBS_USER", "R_PROFILE_USER", "JULIA_PROJECT", "JULIA_DEPOT_PATH", "JULIA_LOAD_PATH", "JUPYTER_PATH"} {
				if v, ok := lookup(env, k); ok {
					t.Errorf("%s=%q", k, v)
				}
			}
			if v, _ := lookup(env, "PATH"); strings.Contains(v, f.cfg.EnvDir) {
				t.Errorf("PATH %q holds the study's folders", v)
			}
		}
	}
}

func TestTwoStudiesGetSeparateFolders(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	c2 := f.newCard(t)
	captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	f.enqueueFor(t, c2)
	waitFor(t, f.settled(t, 2))
	stopDaemon(t, cancel, done)
	studies := filepath.Join(f.cfg.EnvDir, "studies", f.ws.String())
	for _, c := range []uuid.UUID{f.card, c2} {
		if fi, err := os.Stat(filepath.Join(studies, c.String())); err != nil || !fi.IsDir() {
			t.Fatalf("study %s: %v", c, err)
		}
	}
}

// A run on no card keeps its add-on folder beside the studies' folders, never
// in its run folder, where its code could swap it for a link the next run
// would be granted. An attempt's own folder goes when the run ends.
func TestARunWithoutACardKeepsItsFolderOutsideItsRunFolder(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	must(t, f.runs.Store.InWorkspace(context.Background(), f.ws, f.user, func(tx *store.Tx) error {
		_, err := f.runs.EnqueueAutoresearch(context.Background(), tx, f.user, service.AutoresearchAgentRun{Harness: "claude", Prompt: "run only"})
		return err
	}))
	runs := filepath.Join(f.cfg.EnvDir, "studies", f.ws.String(), "_run")
	type seen struct {
		dir     string
		entries []string
	}
	got := make(chan seen, 8)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			es, _ := os.ReadDir(runs)
			var names []string
			for _, e := range es {
				names = append(names, e.Name())
			}
			got <- seen{o.Dir, names}
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			close(msgs)
			done <- agent.Result{}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, f.settled(t, 2))
	stopDaemon(t, cancel, done)
	close(got)
	found := false
	for s := range got {
		if _, err := os.Lstat(filepath.Join(s.dir, ".lyk-envs")); err == nil {
			t.Errorf("a study folder in the run folder %s", s.dir)
		}
		if slices.Contains(s.entries, filepath.Base(s.dir)) {
			found = true
		}
	}
	if !found {
		t.Fatal("the card-less run had no folder of its own under _run")
	}
	if es, _ := os.ReadDir(runs); len(es) != 0 {
		t.Fatalf("left behind: %v", es)
	}
}

func TestRunsDoNotWaitForBases(t *testing.T) {
	f := fixture(t)
	building := make(chan struct{}, 1)
	f.cfg.Bases = &envs.Bases{Dir: t.TempDir(),
		Run: func(context.Context, []string, string, string, ...string) ([]byte, error) {
			return nil, errors.New("unexpected command")
		},
		Fetch: func(ctx context.Context, _, _, _ string) error {
			select {
			case building <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		}}
	f.cfg.EnvDir = f.cfg.Bases.Dir
	got := captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	select {
	case <-building:
	case <-time.After(8 * time.Second):
		t.Fatal("bases never started building")
	}
	l := next(t, got)
	waitFor(t, f.settled(t, 1))
	stopDaemon(t, cancel, done)
	for _, k := range []string{"VIRTUAL_ENV", "R_LIBS_USER", "R_PROFILE_USER", "JULIA_PROJECT", "JULIA_DEPOT_PATH", "JUPYTER_PATH"} {
		if v, ok := lookup(l.env, k); ok {
			t.Fatalf("%s=%q set before any base is ready", k, v)
		}
	}
}

// The CLI may read the study's add-on folder and the bases without asking;
// nothing else outside its run folder is named for it.
func TestTheCLIMayReadTheStudyAndTheBases(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	got := captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	l := next(t, got)
	waitFor(t, f.settled(t, 1))
	stopDaemon(t, cancel, done)
	b := f.cfg.EnvDir
	want := []string{filepath.Join(b, "studies", f.ws.String(), f.card.String()), filepath.Join(b, "python"), filepath.Join(b, "r"), filepath.Join(b, "julia"), filepath.Join(b, "micromamba")}
	if !slices.Equal(l.opts.ReadDirs, want) {
		t.Fatalf("read folders %q, want %q", l.opts.ReadDirs, want)
	}
}
