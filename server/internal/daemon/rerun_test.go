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
)

// rerunServer is a stand-in for Lykeion's server as a rerun meets it: it
// serves attachments, takes outputs, and keeps the rerun's result and any
// failure report.
type rerunServer struct {
	mu          sync.Mutex
	started     bool
	attachments map[uuid.UUID]string
	outputs     map[string]string
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
}

type condaCall struct{ python, lock string }

// newRerun is a rerun of steps over a study with a ready Python base whose
// scripts run under /bin/sh in a stand-in sandbox; adjust may change the
// door before it starts.
func newRerun(t *testing.T, packages string, steps []service.RerunStep, adjust func(*Door)) *rerunCase {
	t.Helper()
	rc := &rerunCase{srv: &rerunServer{attachments: map[uuid.UUID]string{}, outputs: map[string]string{}}, sandbox: &sandboxed{}}
	srv := httptest.NewServer(rc.srv)
	t.Cleanup(srv.Close)
	bases := fakeStudy(t, envs.Python).Bases
	runtimeID, ws, card, owner := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	state := t.TempDir()
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
	rc.c.Rerun = &service.ClaimedRerun{Steps: steps, Packages: packages}
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
	steps := []service.RerunStep{
		{Key: "run1", Path: "src/make.py", Language: envs.Python, Script: "mkdir -p outputs && echo 0.873 > outputs/auc.txt && echo made\n"},
		{Key: "run2", Path: "report.py", Language: envs.Python, Script: "cat outputs/auc.txt\n"},
	}
	rc := newRerun(t, fakePackages, steps, nil)
	before, mtime := listing(t, rc.original)
	rc.run(t)

	if rc.srv.result == nil {
		t.Fatalf("no result posted; fail report %v", rc.srv.fail)
	}
	got := rc.srv.result["steps"].([]any)
	if len(got) != 2 {
		t.Fatalf("steps %v", got)
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
			return "", errors.New("conda-forge has no such build")
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
