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

func TestARunSeesItsStudyEnvironment(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	got := captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	l := next(t, got)
	waitFor(t, f.settled(t, 1))
	stopDaemon(t, cancel, done)
	study := filepath.Join(f.cfg.EnvDir, "studies", f.ws.String(), f.card.String())
	if v, _ := lookup(l.env, "VIRTUAL_ENV"); v != filepath.Join(study, "python") {
		t.Fatalf("VIRTUAL_ENV %q in %q", v, l.env)
	}
	self, err := os.Executable()
	must(t, err)
	sep := string(os.PathListSeparator)
	want := filepath.Dir(self) + sep + filepath.Join(study, "python", "bin") + sep + filepath.Join(f.cfg.EnvDir, "python", "bin") + sep
	if v, _ := lookup(l.env, "PATH"); !strings.HasPrefix(v, want) {
		t.Fatalf("PATH %q, want prefix %q", v, want)
	}
	// The CLI's own environment carries it in both continuity and private mode.
	for _, inherit := range []bool{true, false} {
		o := l.opts
		o.Inherit = inherit
		child := agent.ChildEnv(o)
		if v, _ := lookup(child, "VIRTUAL_ENV"); v != filepath.Join(study, "python") {
			t.Fatalf("inherit %v: child VIRTUAL_ENV %q", inherit, v)
		}
		if v, _ := lookup(child, "PATH"); !strings.HasPrefix(v, want) {
			t.Fatalf("inherit %v: child PATH %q, want prefix %q", inherit, v, want)
		}
	}
}

func TestTwoStudiesGetSeparateFolders(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	c2 := f.newCard(t)
	got := captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	first := next(t, got)
	waitFor(t, f.settled(t, 1))
	f.enqueueFor(t, f.card)
	f.enqueueFor(t, c2)
	second, third := next(t, got), next(t, got)
	waitFor(t, f.settled(t, 3))
	stopDaemon(t, cancel, done)
	var venvs []string
	for _, l := range []launched{first, second, third} {
		v, _ := lookup(l.env, "VIRTUAL_ENV")
		venvs = append(venvs, v)
	}
	slices.Sort(venvs)
	studies := filepath.Join(f.cfg.EnvDir, "studies", f.ws.String())
	want := []string{filepath.Join(studies, f.card.String(), "python"), filepath.Join(studies, f.card.String(), "python"), filepath.Join(studies, c2.String(), "python")}
	slices.Sort(want)
	if !slices.Equal(venvs, want) {
		t.Fatalf("VIRTUAL_ENV values %q, want %q", venvs, want)
	}
}

func TestARunWithoutACardUsesItsRunFolder(t *testing.T) {
	f := fixture(t)
	f.cfg.Bases = readyPython(t)
	f.cfg.EnvDir = f.cfg.Bases.Dir
	must(t, f.runs.Store.InWorkspace(context.Background(), f.ws, f.user, func(tx *store.Tx) error {
		_, err := f.runs.EnqueueAutoresearch(context.Background(), tx, f.user, service.AutoresearchAgentRun{Harness: "claude", Prompt: "run only"})
		return err
	}))
	got := captureLaunches(f)
	cancel, done := startDaemon(t, f.cfg)
	a, b := next(t, got), next(t, got)
	waitFor(t, f.settled(t, 2))
	stopDaemon(t, cancel, done)
	cardVenv := filepath.Join(f.cfg.EnvDir, "studies", f.ws.String(), f.card.String(), "python")
	va, _ := lookup(a.env, "VIRTUAL_ENV")
	if va == cardVenv {
		a, b = b, a
		va, _ = lookup(a.env, "VIRTUAL_ENV")
	}
	if vb, _ := lookup(b.env, "VIRTUAL_ENV"); vb != cardVenv {
		t.Fatalf("card run VIRTUAL_ENV %q, want %q", vb, cardVenv)
	}
	if want := filepath.Join(a.dir, ".lyk-envs", "python"); va != want {
		t.Fatalf("card-less run VIRTUAL_ENV %q, want %q", va, want)
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
