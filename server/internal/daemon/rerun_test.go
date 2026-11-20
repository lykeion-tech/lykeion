package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

// rerunServer is a stand-in for Lykeion's server as a rerun meets it: it
// serves attachments, takes outputs, and keeps the rerun's result and any
// failure report.
type rerunServer struct {
	mu          sync.Mutex
	started     bool
	attachments map[uuid.UUID]string
	outputs     map[string]string
	services    map[string][]byte
	result      map[string]any
	fail        map[string]any
	tokens      []string
}

func (s *rerunServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, r.Header.Get("Authorization"))
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/prepare-lease"):
	case strings.HasSuffix(p, "/start"):
		s.started = true
	case strings.HasPrefix(p, "/api/daemon/runs/") && s.services[p] != nil:
		w.Write(s.services[p])
		return
	case strings.HasPrefix(p, "/api/role/attachments/"):
		id, _ := uuid.Parse(strings.TrimPrefix(p, "/api/role/attachments/"))
		w.Write([]byte(s.attachments[id]))
		return
	case p == "/api/role/outputs":
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(f)
		sum := sha256.Sum256(b)
		s.outputs[r.FormValue("path")] = string(b)
		json.NewEncoder(w).Encode(map[string]any{"name": r.FormValue("path"), "sha256": hex.EncodeToString(sum[:])})
		return
	case strings.HasPrefix(p, "/api/daemon/reruns/") && strings.HasSuffix(p, "/result"):
		if !s.started {
			http.Error(w, "not started", 409)
			return
		}
		json.NewDecoder(r.Body).Decode(&s.result)
	case strings.HasSuffix(p, "/fail"):
		json.NewDecoder(r.Body).Decode(&s.fail)
	default:
		http.NotFound(w, r)
		return
	}
	w.Write([]byte("{}"))
}

type rerunCase struct {
	d       *daemon
	c       claimed
	srv     *rerunServer
	sandbox *sandboxed
	// original is the study's own folder, which a rerun never touches.
	original string
	// conda holds each call of the fake environment rebuild.
	conda []condaCall
	// cleanup undoes what a case's setup changed.
	cleanup []func()
}

type condaCall struct{ python, lock string }

func TestRerunNeverInstallsSharedBases(t *testing.T) {
	for _, lang := range []string{envs.R, envs.Julia} {
		t.Run(lang, func(t *testing.T) {
			root := t.TempDir()
			// Even where installs are possible, a rerun never starts one.
			bases := envs.NewBoundedBases(root)
			bases.Fetch = func(context.Context, string, string, string) error {
				t.Fatal("rerun attempted a download")
				return nil
			}
			study := envs.ForStudy(bases, t.TempDir())
			err := (&daemon{}).rebuildLanguage(t.Context(), &Door{Study: study}, "", lang, "", "")
			if !errors.Is(err, envs.ErrNotReady) {
				t.Fatalf("rerun error = %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rerun changed bases: %v %v", entries, err)
			}
		})
	}
}

// fakeRscript notes each command line it is given in Rscript.log beside
// itself, and lists no installed package.
const fakeRscript = "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$0.log\"\n"

// newRerun is a rerun of steps over a study with ready Python and R bases
// whose scripts run under /bin/sh in a stand-in sandbox. A step with no
// package list of its own saved packages; adjust may change the door
// before it starts.
func newRerun(t *testing.T, packages string, steps []service.RerunStep, adjust func(*Door)) *rerunCase {
	t.Helper()
	rc := &rerunCase{srv: &rerunServer{attachments: map[uuid.UUID]string{}, outputs: map[string]string{}, services: map[string][]byte{}}, sandbox: &sandboxed{}}
	srv := httptest.NewServer(rc.srv)
	t.Cleanup(srv.Close)
	bases := fakeStudy(t, envs.Python, envs.R).Bases
	must(t, os.MkdirAll(filepath.Join(bases.Dir, "r", "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(bases.Dir, "r", "bin", "Rscript"), []byte(fakeRscript), 0o755))
	steps = slices.Clone(steps)
	for i := range steps {
		if steps[i].Packages == "" {
			steps[i].Packages = packages
		}
	}
	runtimeID, ws, card, owner := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	state := t.TempDir()
	// Service code is kept read-only.
	t.Cleanup(func() { agent.RemoveTree(state) })
	rc.d = &daemon{cfg: Config{Server: srv.URL, StateDir: state, Home: t.TempDir()}, bases: bases, ownerOf: map[uuid.UUID]uuid.UUID{runtimeID: owner},
		api: &apiclient.Client{Server: srv.URL, Token: "lyk_daemon"}}
	rc.d.messages = &Outbox{Dir: filepath.Join(state, "messages"), Send: rc.d.sendMessages}
	rc.d.out = &Outbox{Dir: filepath.Join(state, "outbox"), Send: rc.d.send}
	rc.d.setUpConda = func(s *envs.Study, _ context.Context, python string, pkgs []string, lock string) (string, error) {
		b, err := os.ReadFile(lock)
		must(t, err)
		if pkgs != nil {
			t.Errorf("conda packages besides the lock: %v", pkgs)
		}
		rc.conda = append(rc.conda, condaCall{python, string(b)})
		return "", nil
	}
	rc.d.rerunDoor = func(d *Door) {
		d.wrap = stubSandbox(rc.sandbox)
		d.interp = func(lang string) (string, []string, error) {
			if _, _, err := d.Study.Interpreter(lang); err != nil {
				return "", nil, err
			}
			return "/bin/sh", nil, nil
		}
		if adjust != nil {
			adjust(d)
		}
	}
	rc.original = filepath.Join(bases.Dir, "studies", ws.String(), card.String())
	must(t, os.MkdirAll(rc.original, 0o700))
	must(t, os.WriteFile(filepath.Join(rc.original, "kept"), []byte("study"), 0o600))
	past := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(rc.original, past, past))
	rc.c.AgentRun.ID, rc.c.AgentRun.WorkspaceID, rc.c.AgentRun.RuntimeID = uuid.New(), ws, &runtimeID
	rc.c.AgentRun.ResponsibleUserID, rc.c.AgentRun.CardID, rc.c.AgentRun.Attempt, rc.c.AgentRun.Kind = owner, &card, 1, "rerun"
	rc.c.Token = "ltk_rerun_secret"
	rc.c.Rerun = &service.ClaimedRerun{Steps: steps}
	return rc
}

func (rc *rerunCase) run(t *testing.T) {
	t.Helper()
	must(t, rc.d.runAgentRun(t.Context(), rc.c))
	must(t, rc.d.out.Flush(t.Context()))
}

func listing(t *testing.T, dir string) (string, time.Time) {
	t.Helper()
	es, err := os.ReadDir(dir)
	must(t, err)
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	fi, err := os.Stat(dir)
	must(t, err)
	return strings.Join(names, ","), fi.ModTime()
}

func TestRerunReplaysScriptsInAFreshFolder(t *testing.T) {
	measured := []string{"outputs/auc.txt"}
	steps := []service.RerunStep{
		{Key: "run1", Path: "src/make.py", Language: envs.Python, Script: "mkdir -p outputs && echo 0.873 > outputs/auc.txt && echo made\n", OutputPaths: measured},
		{Key: "run2", Path: "report.py", Language: envs.Python, Script: "cat outputs/auc.txt\n", OutputPaths: measured},
		{Key: "run3", Path: "env.py", Language: envs.Python, Script: "env\n"},
	}
	// The daemon's own token is in its environment; neither it nor the
	// rerun's may reach a script.
	t.Setenv("LYK_TOKEN", "lyk_daemon")
	t.Setenv("LYKEION_SECRET_PROBE", "ltk_rerun_secret")
	rc := newRerun(t, fakePackages, steps, nil)
	before, mtime := listing(t, rc.original)
	rc.run(t)

	if rc.srv.result == nil {
		t.Fatalf("no result posted; fail report %v", rc.srv.fail)
	}
	got := rc.srv.result["steps"].([]any)
	if len(got) != 3 {
		t.Fatalf("steps %v", got)
	}
	env := got[2].(map[string]any)["output"].(string)
	if !strings.Contains(env, "PATH=") || strings.Contains(env, "lyk_daemon") || strings.Contains(env, "ltk_rerun_secret") {
		t.Fatalf("a token reached a script, or env did not run: %q", env)
	}
	first, second := got[0].(map[string]any), got[1].(map[string]any)
	if first["key"] != "run1" || first["output"] != "made\n" || first["exit_status"] != 0.0 {
		t.Fatalf("first step %v", first)
	}
	if second["key"] != "run2" || second["output"] != "0.873\n" || second["exit_status"] != 0.0 {
		t.Fatalf("the second step did not read what the first wrote: %v", second)
	}
	if rc.srv.result["failure"] != nil {
		t.Fatalf("failure %v", rc.srv.result["failure"])
	}
	sum := sha256.Sum256([]byte("0.873\n"))
	firstHashes, _ := first["output_hashes"].(map[string]any)
	secondHashes, ok := second["output_hashes"].(map[string]any)
	if firstHashes["outputs/auc.txt"] != hex.EncodeToString(sum[:]) || !ok || len(secondHashes) != 0 {
		t.Fatalf("artifact attribution did not distinguish producing and reading: %v %v", first["output_hashes"], second["output_hashes"])
	}
	outputs := rc.srv.result["outputs"].([]any)
	if len(outputs) != 1 || outputs[0].(map[string]any)["name"] != "outputs/auc.txt" || outputs[0].(map[string]any)["text_sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("outputs %v", outputs)
	}
	if rc.srv.outputs["outputs/auc.txt"] != "0.873\n" {
		t.Fatalf("uploaded %v", rc.srv.outputs)
	}
	for _, tok := range rc.srv.tokens {
		if tok == "Bearer ltk_rerun_secret" {
			continue
		}
		if tok != "Bearer lyk_daemon" {
			t.Fatalf("unexpected credential %q", tok)
		}
	}
	commands, _ := rc.sandbox.seen()
	ran := 0
	for _, c := range commands {
		if c[0] == "/bin/sh" && len(c) == 2 && (strings.HasSuffix(c[1], "src/make.py") || strings.HasSuffix(c[1], "report.py")) {
			ran++
			if strings.HasPrefix(c[1], rc.original) {
				t.Fatalf("a step ran in the study's own folder: %v", c)
			}
		}
	}
	if ran != 2 {
		t.Fatalf("steps outside the sandbox: %v", commands)
	}
	if after, mt := listing(t, rc.original); after != before || !mt.Equal(mtime) {
		t.Fatalf("the study's folder changed: %q at %v, was %q at %v", after, mt, before, mtime)
	}
	if _, err := os.Stat(filepath.Join(rc.d.cfg.Home, scratchName(rc.c.AgentRun.ID, 1))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the rerun's folder is left: %v", err)
	}
}

func TestRerunRunsInsideTheSandbox(t *testing.T) {
	steps := []service.RerunStep{{Key: "run1", Path: "a.py", Language: envs.Python, Script: "touch ran\necho 1\n"}}
	for name, adjust := range map[string]func(*Door){
		"failed its check": func(d *Door) { d.SandboxErr = errors.New("install bubblewrap") },
		"won't start": func(d *Door) {
			d.wrap = func(context.Context, sandbox.Policy, string, ...string) (*exec.Cmd, error) {
				return nil, errors.New("install bubblewrap")
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			rc := newRerun(t, fakePackages, steps, adjust)
			rc.run(t)
			if rc.srv.result != nil {
				t.Fatalf("a result was posted: %v", rc.srv.result)
			}
			if rc.srv.fail == nil || rc.srv.fail["failure_reason"] != "sandbox" {
				t.Fatalf("fail report %v", rc.srv.fail)
			}
			if len(rc.conda) != 0 {
				t.Fatalf("the environment was rebuilt: %v", rc.conda)
			}
			if commands, _ := rc.sandbox.seen(); len(commands) != 0 {
				t.Fatalf("ran %v", commands)
			}
		})
	}
}

func TestRerunEnvironmentFromSavedPackages(t *testing.T) {
	steps := []service.RerunStep{{Key: "run1", Path: "a.py", Language: envs.Python, Script: "touch ran\necho 1\n"}}
	t.Run("rebuilt from the lock", func(t *testing.T) {
		rc := newRerun(t, "# python base\n@EXPLICIT\nhttps://conda/python-3.11.tar.bz2\n\n# python study\nrequests==2.32.0\n", steps, nil)
		rc.c.Rerun.Python = "3.11"
		rc.d.setUpConda = func(_ *envs.Study, _ context.Context, python string, _ []string, lock string) (string, error) {
			b, err := os.ReadFile(lock)
			must(t, err)
			rc.conda = append(rc.conda, condaCall{python, string(b)})
			return "error    libmamba Could not solve for environment specs", errors.New("exit status 1")
		}
		rc.run(t)
		if len(rc.conda) != 1 || rc.conda[0].python != "3.11" || rc.conda[0].lock != "@EXPLICIT\nhttps://conda/python-3.11.tar.bz2\n" {
			t.Fatalf("conda %+v", rc.conda)
		}
		if rc.srv.result == nil || rc.srv.result["failure"] != "environment" {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
		if steps := rc.srv.result["steps"]; steps != nil && len(steps.([]any)) != 0 {
			t.Fatalf("steps ran in an environment that failed: %v", steps)
		}
	})
	t.Run("a refusal that names packages like network words", func(t *testing.T) {
		rc := newRerun(t, fakePackages, steps, nil)
		rc.d.setUpConda = func(*envs.Study, context.Context, string, []string, string) (string, error) {
			return "error    libmamba Could not solve for environment specs\n    - nothing provides pyopenssl >=99 needed by async-timeout 4.0, proxy_tools 0.1", errors.New("exit status 1")
		}
		rc.run(t)
		if rc.srv.result == nil || rc.srv.result["failure"] != "environment" {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
	})
	t.Run("the study's own packages are installed", func(t *testing.T) {
		rc := newRerun(t, fakePackages, steps, nil)
		rc.run(t)
		if len(rc.conda) != 1 || rc.conda[0].lock != "python-lock\n" {
			t.Fatalf("conda %+v", rc.conda)
		}
		commands, _ := rc.sandbox.seen()
		i := slices.IndexFunc(commands, func(c []string) bool { return slices.Contains(c, "pip") && slices.Contains(c, "install") })
		if i < 0 {
			t.Fatalf("no pip install in %v", commands)
		}
		if rc.srv.result == nil || rc.srv.result["failure"] != nil {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
	})
	for name, packages := range map[string]string{
		"no saved list":          "",
		"a list of another kind": "requests==2.32.0\n",
		"another language's":     "# r base\nr-lock\n\n# r study\n",
	} {
		t.Run(name, func(t *testing.T) {
			rc := newRerun(t, packages, steps, nil)
			rc.run(t)
			if rc.srv.result == nil || rc.srv.result["failure"] != "environment" {
				t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
			}
			if commands, _ := rc.sandbox.seen(); slices.ContainsFunc(commands, func(c []string) bool { return c[0] == "/bin/sh" && len(c) == 2 }) {
				t.Fatalf("a step ran: %v", commands)
			}
		})
	}
}

func TestRerunAttachmentsReadOnly(t *testing.T) {
	steps := []service.RerunStep{{Key: "run1", Path: "a.py", Language: envs.Python, Script: "echo changed > attachments/data.csv\ns=$?\ncat attachments/data.csv\nexit $s\n"}}
	rc := newRerun(t, fakePackages, steps, nil)
	a := attachmentClaim("data.csv", "x,y\n1,2\n", 0, 0)
	rc.srv.attachments[a.ID] = "x,y\n1,2\n"
	rc.c.Rerun.Attachments = []service.ClaimedAttachment{a}
	rc.run(t)
	if rc.srv.result == nil {
		t.Fatalf("no result; fail %v", rc.srv.fail)
	}
	step := rc.srv.result["steps"].([]any)[0].(map[string]any)
	if step["exit_status"] == 0.0 || step["output"] != "x,y\n1,2\n" {
		t.Fatalf("the step changed an attachment: %v", step)
	}
	_, policies := rc.sandbox.seen()
	dir := filepath.Join(rc.d.cfg.Home, scratchName(rc.c.AgentRun.ID, 1), "attachments")
	if len(policies) == 0 || !slices.ContainsFunc(policies[len(policies)-1].ReadOnly, func(p string) bool { return p == dir }) {
		t.Fatalf("attachments are not read-only in the sandbox")
	}
}

// TestRerunMachineFailuresAreRetried: a cause on the machine is never the
// saved list's: it fails the run with an infrastructure reason, which the
// server retries, and posts no result.
func TestRerunMachineFailuresAreRetried(t *testing.T) {
	steps := []service.RerunStep{{Key: "run1", Path: "a.py", Language: envs.Python, Script: "echo 1\n"}}
	for name, tc := range map[string]struct {
		setup  func(*rerunCase)
		reason string
	}{
		"no language bases": {func(rc *rerunCase) { rc.d.bases = nil }, "runtime_offline"},
		"conda can't reach its channel": {func(rc *rerunCase) {
			rc.d.setUpConda = func(*envs.Study, context.Context, string, []string, string) (string, error) {
				return "Could not resolve host: conda.anaconda.org", errors.New("exit status 1")
			}
		}, "runtime_offline"},
		"conda fails for no stated reason": {func(rc *rerunCase) {
			rc.d.setUpConda = func(*envs.Study, context.Context, string, []string, string) (string, error) {
				return "", errors.New("signal: killed")
			}
		}, "runtime_offline"},
		"the setup runs past its limit": {func(rc *rerunCase) {
			rc.d.rebuildLimit = 50 * time.Millisecond
			rc.d.setUpConda = func(_ *envs.Study, ctx context.Context, _ string, _ []string, _ string) (string, error) {
				<-ctx.Done()
				return "", errors.New("signal: killed")
			}
		}, "timeout"},
		"the interpreter won't start": {func(rc *rerunCase) {
			rc.d.rerunDoor = chainDoor(rc.d.rerunDoor, func(d *Door) {
				d.interp = func(string) (string, []string, error) { return "/nonexistent/interpreter", nil, nil }
			})
		}, "runtime_offline"},
		"the R install is refused the sandbox": {rInstall(func(ctx context.Context) (*exec.Cmd, error) {
			return nil, errors.New("install bubblewrap")
		}), "sandbox"},
		"the R install won't start": {rInstall(func(ctx context.Context) (*exec.Cmd, error) {
			return exec.CommandContext(ctx, "/nonexistent/Rscript"), nil
		}), "runtime_offline"},
		"the R install is killed": {rInstall(func(ctx context.Context) (*exec.Cmd, error) {
			return exec.CommandContext(ctx, "/bin/sh", "-c", "kill -9 $$"), nil
		}), "runtime_offline"},
		"the lock was saved on another platform": {func(rc *rerunCase) {
			rc.c.Rerun.Steps[0].Packages = "# python base\n# platform: " + otherPlatform() + "\n@EXPLICIT\nhttps://conda/python.tar.bz2\n\n# python study\n"
		}, "runtime_offline"},
		"this machine's R base is another": {func(rc *rerunCase) {
			rInstall(func(ctx context.Context) (*exec.Cmd, error) { return exec.CommandContext(ctx, "/usr/bin/true"), nil })(rc)
			rc.c.Rerun.Steps[0].Packages = "# r base\nanother-r-lock\n\n# r study\n"
		}, "runtime_offline"},
	} {
		t.Run(name, func(t *testing.T) {
			rc := newRerun(t, fakePackages, steps, nil)
			tc.setup(rc)
			defer func() {
				for _, f := range rc.cleanup {
					f()
				}
			}()
			rc.run(t)
			if rc.srv.result != nil {
				t.Fatalf("posted %v", rc.srv.result)
			}
			if rc.srv.fail == nil || rc.srv.fail["failure_reason"] != tc.reason {
				t.Fatalf("fail report %v, want reason %s", rc.srv.fail, tc.reason)
			}
		})
	}
}

func chainDoor(a, b func(*Door)) func(*Door) { return func(d *Door) { a(d); b(d) } }

// otherPlatform is a conda platform this machine is not.
func otherPlatform() string {
	if here, _ := envs.CondaPlatform(); here == "linux-64" {
		return "osx-arm64"
	}
	return "linux-64"
}

// rInstall makes the rerun an R step whose saved packages CRAN has, and
// starts the sandboxed install of them with install instead.
func rInstall(install func(context.Context) (*exec.Cmd, error)) func(*rerunCase) {
	return func(rc *rerunCase) {
		srv := httptest.NewServer(&cran{files: map[string]string{"/src/contrib/ggplot2_3.4.0.tar.gz": "gg"}})
		old := cranURL
		cranURL = srv.URL
		rc.cleanup = append(rc.cleanup, func() { cranURL = old; srv.Close() })
		rc.c.Rerun.Steps = []service.RerunStep{{Key: "run1", Path: "plot.R", Language: envs.R, Script: "echo r\n", Packages: "# r base\nr-lock\n\n# r study\nggplot2 3.4.0\n"}}
		rc.d.rerunDoor = chainDoor(rc.d.rerunDoor, func(d *Door) {
			stub := d.wrap
			d.wrap = func(ctx context.Context, p sandbox.Policy, name string, args ...string) (*exec.Cmd, error) {
				if slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "install.packages") }) {
					return install(ctx)
				}
				return stub(ctx, p, name, args...)
			}
		})
	}
}

// cran stands in for CRAN, serving only the files it holds and noting every
// path asked for.
type cran struct {
	mu    sync.Mutex
	files map[string]string
	asked []string
	down  bool
}

func (c *cran) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, r.URL.Path)
	if c.down {
		http.Error(w, "unavailable", 503)
		return
	}
	b, ok := c.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write([]byte(b))
}

func TestRerunRebuildsEachLanguageFromItsOwnList(t *testing.T) {
	const rList = "# r base\nr-lock\n\n# r study\nggplot2 3.4.0\noldpkg 1.0\n"
	steps := func() []service.RerunStep {
		return []service.RerunStep{
			{Key: "run1", Path: "fit.py", Language: envs.Python, Script: "echo py\n", Packages: fakePackages},
			{Key: "run2", Path: "plot.R", Language: envs.R, Script: "echo r\n", Packages: rList},
		}
	}
	serve := func(t *testing.T, c *cran) {
		srv := httptest.NewServer(c)
		t.Cleanup(srv.Close)
		old := cranURL
		cranURL = srv.URL
		t.Cleanup(func() { cranURL = old })
	}
	t.Run("each package at its saved version", func(t *testing.T) {
		c := &cran{files: map[string]string{"/src/contrib/ggplot2_3.4.0.tar.gz": "gg", "/src/contrib/Archive/oldpkg/oldpkg_1.0.tar.gz": "old"}}
		serve(t, c)
		rc := newRerun(t, "", steps(), nil)
		rc.run(t)
		if rc.srv.result == nil || rc.srv.result["failure"] != nil || len(rc.srv.result["steps"].([]any)) != 2 {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
		if len(rc.conda) != 1 || rc.conda[0].lock != "python-lock\n" {
			t.Fatalf("python was not rebuilt from its own list: %+v", rc.conda)
		}
		want := []string{"/src/contrib/ggplot2_3.4.0.tar.gz", "/src/contrib/oldpkg_1.0.tar.gz", "/src/contrib/Archive/oldpkg/oldpkg_1.0.tar.gz"}
		if !slices.Equal(c.asked, want) {
			t.Fatalf("asked CRAN for %q, want %q", c.asked, want)
		}
		log, err := os.ReadFile(filepath.Join(rc.d.bases.Dir, "r", "bin", "Rscript.log"))
		must(t, err)
		i := strings.Index(string(log), "install.packages")
		if i < 0 || !strings.Contains(string(log)[i:], "saved-r/ggplot2_3.4.0.tar.gz") || !strings.Contains(string(log)[i:], "saved-r/oldpkg_1.0.tar.gz") {
			t.Fatalf("Rscript ran %s", log)
		}
		commands, _ := rc.sandbox.seen()
		if !slices.ContainsFunc(commands, func(c []string) bool {
			return strings.HasSuffix(c[0], "Rscript") && slices.ContainsFunc(c, func(a string) bool { return strings.Contains(a, "install.packages") })
		}) {
			t.Fatalf("the R packages were not installed in the sandbox: %v", commands)
		}
	})
	t.Run("a package named twice is fetched once", func(t *testing.T) {
		c := &cran{files: map[string]string{"/src/contrib/ggplot2_3.4.0.tar.gz": "gg", "/src/contrib/Archive/oldpkg/oldpkg_1.0.tar.gz": "old"}}
		serve(t, c)
		s := steps()
		s[1].Packages = "# r base\nr-lock\n\n# r study\nggplot2 3.4.0\nggplot2 3.4.0\noldpkg 1.0\n"
		rc := newRerun(t, "", s, nil)
		rc.run(t)
		if rc.srv.result == nil || rc.srv.result["failure"] != nil || strings.Count(strings.Join(c.asked, " "), "ggplot2") != 1 {
			t.Fatalf("result %v fail %v asked %q", rc.srv.result, rc.srv.fail, c.asked)
		}
	})
	t.Run("a package over the size cap", func(t *testing.T) {
		serve(t, &cran{files: map[string]string{"/src/contrib/ggplot2_3.4.0.tar.gz": "gg", "/src/contrib/oldpkg_1.0.tar.gz": "0123456789"}})
		old := maxCRANBytes
		maxCRANBytes = 4
		t.Cleanup(func() { maxCRANBytes = old })
		rc := newRerun(t, "", steps(), nil)
		rc.run(t)
		if rc.srv.result == nil || rc.srv.result["failure"] != "environment" {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
	})
	t.Run("a version CRAN never had", func(t *testing.T) {
		c := &cran{files: map[string]string{"/src/contrib/ggplot2_3.4.0.tar.gz": "gg"}}
		serve(t, c)
		rc := newRerun(t, "", steps(), nil)
		rc.run(t)
		if rc.srv.result == nil || rc.srv.result["failure"] != "environment" {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
	})
	t.Run("CRAN unreachable", func(t *testing.T) {
		serve(t, &cran{down: true})
		rc := newRerun(t, "", steps(), nil)
		rc.run(t)
		if rc.srv.result != nil || rc.srv.fail == nil || rc.srv.fail["failure_reason"] != "runtime_offline" {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
	})
	t.Run("a language with no list of its own", func(t *testing.T) {
		s := steps()
		s[1].Packages = fakePackages
		rc := newRerun(t, "", s, nil)
		rc.run(t)
		if rc.srv.result == nil || rc.srv.result["failure"] != "environment" {
			t.Fatalf("result %v fail %v", rc.srv.result, rc.srv.fail)
		}
	})
}

func TestRerunKeepsAnEarlierAttemptsOutputs(t *testing.T) {
	steps := []service.RerunStep{{Key: "run1", Path: "a.py", Language: envs.Python, Script: "mkdir -p outputs && echo 1 > outputs/auc.txt && echo 2 > outputs/new.txt\n"}}
	rc := newRerun(t, fakePackages, steps, nil)
	rc.c.AgentRun.Attempt = 2
	rc.c.Rerun.Saved = []service.SavedOutput{{Name: "outputs/auc.txt", Sha256: "earlier"}}
	rc.run(t)
	if _, again := rc.srv.outputs["outputs/auc.txt"]; again || rc.srv.outputs["outputs/new.txt"] != "2\n" {
		t.Fatalf("uploaded %v", rc.srv.outputs)
	}
	sum := sha256.Sum256([]byte("2\n"))
	got, _ := json.Marshal(rc.srv.result["outputs"])
	want, _ := json.Marshal([]map[string]string{{"name": "outputs/auc.txt", "text_sha256": "earlier"}, {"name": "outputs/new.txt", "text_sha256": hex.EncodeToString(sum[:])}})
	if string(got) != string(want) {
		t.Fatalf("outputs %s, want %s", got, want)
	}
}

func TestRerunRefusesProtectedPaths(t *testing.T) {
	var steps []service.RerunStep
	for _, p := range []string{".lyk-env/bin/python", "attachments/x.py", "CLAUDE.md", ".claude/x.py", "src/../.lyk-env/x.py", "fine.py"} {
		steps = append(steps, service.RerunStep{Key: p, Path: p, Language: envs.Python, Script: "echo ran\n"})
	}
	rc := newRerun(t, fakePackages, steps, nil)
	rc.run(t)
	if rc.srv.result == nil {
		t.Fatalf("fail %v", rc.srv.fail)
	}
	for _, s := range rc.srv.result["steps"].([]any) {
		step := s.(map[string]any)
		ran := step["exit_status"] == 0.0 && step["output"] == "ran\n"
		if ran != (step["key"] == "fine.py") || (!ran && !strings.Contains(step["error"].(string), "kept for the run")) {
			t.Fatalf("step %v", step)
		}
	}
}

func TestRerunStartsTheStudysServices(t *testing.T) {
	steps := []service.RerunStep{{Key: "run1", Path: "a.py", Language: envs.Python, Script: "echo \"$LYK_SERVICE_EVALUATOR_URL\"\n"}}
	rc := newRerun(t, fakePackages, steps, nil)
	blob := serviceArchive(t)
	sum := sha256.Sum256(blob)
	spec := service.ClaimedService{ID: uuid.New(), Name: "evaluator", Start: "exec ./helper", Sha256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(blob))}
	rc.c.Services = []service.ClaimedService{spec}
	rc.srv.services["/api/daemon/runs/"+rc.c.AgentRun.ID.String()+"/services/"+spec.ID.String()] = blob
	rc.d.serviceReadyLimit, rc.d.serviceStopLimit = 10*time.Second, 50*time.Millisecond
	// As in the service tests: the real filesystem sandbox, its default network.
	rc.d.serviceCommand = func(p sandbox.Policy, ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
		p.LoopbackOnly = false
		return p.Command(ctx, name, args...)
	}
	rc.run(t)
	if rc.srv.result == nil {
		t.Fatalf("fail %v", rc.srv.fail)
	}
	step := rc.srv.result["steps"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(step["output"].(string), "http://127.0.0.1:") {
		t.Fatalf("the step did not get the service: %v", step)
	}
}
