package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

// rerunResult is what a rerun posts: each replayed step, the files its
// scripts saved, and why it could not replay, when it could not.
type rerunResult struct {
	Steps   []rerunStep   `json:"steps"`
	Outputs []rerunOutput `json:"outputs"`
	Failure *string       `json:"failure"`
}

type rerunStep struct {
	Key        string `json:"key"`
	ExitStatus int    `json:"exit_status"`
	Output     string `json:"output"`
	Error      string `json:"error"`
}

type rerunOutput struct {
	Name       string `json:"name"`
	TextSHA256 string `json:"text_sha256"`
}

// failEnvironment is the result's failure when the saved package list can't
// rebuild the environment the scripts ran in.
const failEnvironment = "environment"

// runRerun replays a study's recorded scripts, in the order they ran, in a
// fresh folder and a fresh environment rebuilt from the saved package list,
// inside the sandbox, with the study's attachments read-only beside them.
// The study's own folders are never touched. No agent CLI runs, and the
// rerun's token stays with the daemon: it fetches the attachments and saves
// the outputs, and no script sees it.
func (d *daemon) runRerun(ctx context.Context, c claimed) (localErr error) {
	var runtimeID uuid.UUID
	if c.AgentRun.RuntimeID != nil {
		runtimeID = *c.AgentRun.RuntimeID
	}
	r := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: runtimeID, Attempt: c.AgentRun.Attempt, Kind: "fail", FailureReason: agent.ReasonRuntimeOffline}
	// Journal before the server can enter running.
	if err := d.journal().Put(r); err != nil {
		return err
	}
	fail := func(err error) { failReport(&r, "", err) }
	dir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
	defer func() {
		if ctx.Err() != nil {
			r.Kind, r.Rerun, r.Error, r.FailureReason = "fail", nil, ctx.Err().Error(), agent.ReasonRuntimeOffline
		}
		// The rebuilt environment can be gigabytes; what the rerun keeps
		// was saved as outputs.
		if err := agent.RemoveTree(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("removing a rerun's folder", "dir", dir, "err", err)
		}
		if err := d.out.Put(r); err != nil {
			localErr = errors.Join(localErr, err)
			return
		}
		localErr = errors.Join(localErr, d.forgetAttempt(r))
	}()
	d.mu.Lock()
	owner, knownOwner := d.ownerOf[r.RuntimeID]
	d.mu.Unlock()
	if !knownOwner || owner == uuid.Nil {
		fail(errors.New("unknown runtime owner"))
		return nil
	}
	if c.Rerun == nil || c.AgentRun.CardID == nil {
		fail(errors.New("the rerun names no study or scripts"))
		return nil
	}
	guest := c.AgentRun.ResponsibleUserID != owner
	if guest && runtime.GOOS != "linux" {
		fail(errors.New("shared runs require Linux: process environment isolation is not enforced"))
		return nil
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		fail(fmt.Errorf("prepare scratch: %w", err))
		return nil
	}
	base := "/api/daemon/agent-runs/" + r.AgentRunID.String()
	ref := map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt}
	for _, step := range []string{"/prepare-lease", "/start"} {
		if err := d.api.Do(ctx, "POST", base+step, ref, nil); err != nil {
			fail(err)
			if retryableControlLoss(err) {
				r.FailureReason = agent.ReasonRuntimeOffline
			}
			return nil
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func(ref Report) { defer close(watched); d.watchCancel(runCtx, cancel, base, ref) }(r)
	defer func() {
		if runCtx.Err() != nil && ctx.Err() == nil {
			// The server cancelled the rerun.
			r.Kind, r.Rerun, r.Error = "fail", nil, "the rerun was cancelled"
			if err := d.api.Do(ctx, "POST", base+"/cancel-ack", ref, nil); err != nil {
				slog.Warn("cancellation acknowledgement pending", "run", r.AgentRunID, "attempt", r.Attempt, "err", err)
			}
		}
		cancel()
		<-watched
	}()
	result := &rerunResult{Steps: []rerunStep{}, Outputs: []rerunOutput{}}
	settle := func(failure string) {
		if failure != "" {
			result.Failure = &failure
		}
		r.Kind, r.Rerun, r.Error, r.FailureReason = "rerun", result, "", ""
	}
	unsandboxed := func(err error) {
		r.Kind, r.Error, r.FailureReason = "fail", err.Error(), agent.ReasonSandbox
	}

	files := c
	files.Attachments = c.Rerun.Attachments
	if err := d.placeAttachments(runCtx, files, dir); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	if d.bases == nil {
		settle(failEnvironment)
		return nil
	}
	// The environment is the rerun's own, beside its scripts and gone with
	// them.
	study := envs.ForStudy(d.bases, filepath.Join(dir, ".lyk-env"))
	study.PythonVersion = c.Rerun.Python
	if err := os.Mkdir(study.Dir, 0o700); err != nil {
		fail(err)
		return nil
	}
	private, err := d.privatePaths()
	if err != nil {
		fail(err)
		return nil
	}
	opts := agent.ExecOptions{Dir: dir}
	door := &Door{Guest: guest, Server: d.cfg.Server, Study: study, PrivatePaths: private, PrivateRoots: []string{d.cfg.Home}, SandboxErr: d.sandboxErr,
		Child: func() agent.ExecOptions {
			if guest {
				return guestDoorOptions(opts)
			}
			return opts
		}}
	if d.rerunDoor != nil {
		d.rerunDoor(door)
	}
	if err := door.Start(); err != nil {
		fail(err)
		return nil
	}
	defer door.Close()
	// The rerun keeps the study's Python as given; the server already has it.
	door.Study.PersistPython = nil
	// No sandbox, no rerun: nothing is built or run before it is known to work.
	if _, err := door.command(runCtx, "/usr/bin/true"); err != nil {
		unsandboxed(err)
		return nil
	}
	// ended settles a rerun that err stopped: the sandbox's failure, the
	// run's ending, or an environment that can't run the scripts.
	ended := func(err error) {
		var sandboxErr unsandboxedError
		switch {
		case errors.As(err, &sandboxErr):
			unsandboxed(err)
		case runCtx.Err() != nil:
		default:
			slog.Warn("rerun environment", "run", r.AgentRunID, "err", err)
			settle(failEnvironment)
		}
	}
	if err := d.rebuild(runCtx, door, c.Rerun); err != nil {
		ended(err)
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		fail(err)
		return nil
	}
	defer root.Close()
	// ponytail: each step gets an even share of the body limit, cut from its
	// tail; share by need if one step's output crowds out the others.
	share := maxRunBody / max(len(c.Rerun.Steps), 1)
	for _, s := range c.Rerun.Steps {
		// A step is written just before it runs: a script may have changed
		// between two runs of the same path.
		err := root.MkdirAll(filepath.Dir(s.Path), 0o700)
		if err == nil {
			err = root.WriteFile(s.Path, []byte(s.Script), 0o600)
		}
		var output, errText string
		status := -1
		if err != nil {
			errText = "the script could not be written: " + err.Error()
		} else if output, errText, status, err = door.execScript(runCtx, door.Study, s.Path, s.Language, s.Script); err != nil {
			ended(err)
			return nil
		}
		body := map[string]any{"key": s.Key, "exit_status": status, "output": output, "error": errText}
		fitBody(body, share)
		result.Steps = append(result.Steps, rerunStep{Key: s.Key, ExitStatus: status, Output: body["output"].(string), Error: body["error"].(string)})
	}
	role := &apiclient.Client{Server: d.cfg.Server, Token: c.Token, HTTP: &http.Client{}}
	saved, sums := map[string]bool{}, map[string]string{}
	// A file the server's limits refuse is left out: a value only it held is
	// not reproduced.
	if _, err := sendOutputs(runCtx, role, dir, func() (map[string]bool, error) { return saved, nil }, sums); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(sums)) {
		result.Outputs = append(result.Outputs, rerunOutput{Name: name, TextSHA256: sums[name]})
	}
	settle("")
	return nil
}

// rebuild makes the rerun's fresh environment, for the languages its steps
// use, from the saved package list, which holds one language's: its base and
// what the study added. A language the list doesn't hold, or holds in a form
// that can't be rebuilt, is an error.
//   - Python: a conda environment from the saved lock, then the study's own
//     packages at the versions pip froze.
//   - R: the machine's base, which must be the one saved; packages the study
//     installed can't be rebuilt from their names and versions alone.
//   - Julia: the machine's base, which must be the one saved, and the
//     study's saved Manifest.toml, instantiated.
func (d *daemon) rebuild(ctx context.Context, door *Door, rerun *service.ClaimedRerun) error {
	lang, base, own, ok := savedPackages(rerun.Packages)
	if !ok {
		return errors.New("the saved package list is missing or unreadable")
	}
	for _, s := range rerun.Steps {
		if s.Language != lang {
			return fmt.Errorf("no package list was saved for %s", s.Language)
		}
	}
	study := door.Study
	switch lang {
	case envs.Python:
		lock := filepath.Join(study.Dir, "saved-lock.txt")
		if err := os.WriteFile(lock, []byte(base), 0o600); err != nil {
			return err
		}
		setUp := d.setUpConda
		if setUp == nil {
			setUp = (*envs.Study).SetUpConda
		}
		if out, err := setUp(study, ctx, rerun.Python, nil, lock); err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		if strings.TrimSpace(own) == "" {
			return nil
		}
		reqs := filepath.Join(study.Dir, "saved-requirements.txt")
		if err := os.WriteFile(reqs, []byte(own), 0o600); err != nil {
			return err
		}
		python, _, err := study.Interpreter(envs.Python)
		if err != nil {
			return err
		}
		env, err := study.Environment(ctx)
		if err != nil {
			return err
		}
		_, err = door.runSandboxed(ctx, env, study.Dir, python, "-m", "pip", "install", "--no-deps", "--no-input", "--disable-pip-version-check", "-r", reqs)
		return err
	case envs.R, envs.Julia:
		if err := study.Bases.Ensure(ctx, lang); err != nil {
			return err
		}
		fresh, err := study.Packages(ctx, lang)
		if err != nil {
			return err
		}
		if _, now, _, _ := savedPackages(fresh); now != base {
			return fmt.Errorf("this machine's %s base is not the one the study ran on", lang)
		}
		if strings.TrimSpace(own) == "" {
			return nil
		}
		if lang == envs.R {
			return errors.New("R packages the study installed can't be rebuilt from the saved list")
		}
		env, err := study.Environment(ctx)
		if err != nil {
			return err
		}
		project := filepath.Join(study.Dir, "julia")
		if err := os.WriteFile(filepath.Join(project, "Manifest.toml"), []byte(own), 0o600); err != nil {
			return err
		}
		julia, _, err := study.Interpreter(envs.Julia)
		if err != nil {
			return err
		}
		_, err = door.runSandboxed(ctx, env, project, julia, "-e", instantiateManifest)
		return err
	}
	return fmt.Errorf("unknown language %q", lang)
}

// instantiateManifest makes every package of the saved Manifest.toml a
// dependency of the project beside it, which the saved list doesn't keep,
// so scripts can load them, and installs them at the manifest's versions.
const instantiateManifest = `using TOML, Pkg
m = TOML.parsefile("Manifest.toml")
deps = haskey(m, "manifest_format") ? get(m, "deps", Dict()) : m
open("Project.toml", "w") do io
    TOML.print(io, Dict("deps" => Dict(k => v[1]["uuid"] for (k, v) in deps if v isa Vector && haskey(v[1], "uuid"))))
end
Pkg.instantiate()`

// savedPackages splits a saved package list, as envs.Study.Packages writes
// it, into its language, its base's lock and what the study added.
func savedPackages(list string) (lang, base, own string, ok bool) {
	head, rest, ok := strings.Cut(list, "\n")
	if !ok {
		return "", "", "", false
	}
	if lang, ok = strings.CutPrefix(head, "# "); !ok {
		return "", "", "", false
	}
	if lang, ok = strings.CutSuffix(lang, " base"); !ok {
		return "", "", "", false
	}
	base, own, ok = strings.Cut(rest, "\n# "+lang+" study\n")
	return lang, base, own, ok
}
