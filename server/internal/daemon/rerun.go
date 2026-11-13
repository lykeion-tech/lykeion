package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

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

// failEnvironment is the result's failure when the saved package lists
// can't rebuild the environment the scripts ran in.
const failEnvironment = "environment"

// rerunEnv is the folder, inside the rerun's own, that holds its rebuilt
// environment; no replayed script is written there.
const rerunEnv = ".lyk-env"

// unrebuildable is why the saved package lists can't rebuild the
// environment: retrying on this machine would end the same way. Every other
// failure to rebuild is the machine's, and the rerun is tried again.
type unrebuildable struct{ error }

func (u unrebuildable) Unwrap() error { return u.error }

// otherMachine is a saved package list made on another kind of machine:
// another platform, or another language base. A machine like the study's
// can rebuild it, so it is this machine's cause, never the study's.
type otherMachine struct{ error }

func (o otherMachine) Unwrap() error { return o.error }

// cranURL is where R packages are fetched at their saved versions, with
// cranClient, each at most maxCRANBytes.
var (
	cranURL      = "https://cloud.r-project.org"
	cranClient   = &http.Client{Timeout: 10 * time.Minute}
	maxCRANBytes = int64(200 << 20)
)

// runRerun replays a study's recorded scripts, in the order they ran, in a
// fresh folder and a fresh environment rebuilt from each step's saved
// package list, inside the sandbox, with the study's attachments read-only
// and its services running beside them. The study's own folders are never
// touched. No agent CLI runs, and the rerun's token stays with the daemon:
// it fetches the attachments and saves the outputs, and no script sees it.
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
	// machine fails the rerun for a cause on this machine, which the server
	// retries and never holds against the study.
	machine := func(reason string, err error) {
		r.Kind, r.Rerun, r.Error, r.FailureReason = "fail", nil, err.Error(), reason
	}
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
		machine(agent.ReasonRuntimeOffline, fmt.Errorf("prepare scratch: %w", err))
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
	// ended settles a rerun that err stopped. Only a saved list that can't
	// rebuild the environment is the study's; the run's own end reports
	// nothing more, and every other cause is this machine's.
	ended := func(err error) {
		var sandboxErr unsandboxedError
		var refused unrebuildable
		var other otherMachine
		switch {
		case errors.As(err, &sandboxErr):
			machine(agent.ReasonSandbox, err)
		case errors.Is(err, context.DeadlineExceeded):
			machine("timeout", err)
		case runCtx.Err() != nil:
		case errors.As(err, &other):
			machine(agent.ReasonRuntimeOffline, err)
		case errors.As(err, &refused):
			slog.Warn("rerun environment", "run", r.AgentRunID, "err", err)
			settle(failEnvironment)
		default:
			machine(agent.ReasonRuntimeOffline, err)
		}
	}

	files := c
	files.Attachments = c.Rerun.Attachments
	if _, err := d.placeAttachments(runCtx, files, dir); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	// No sandbox, no rerun: nothing is built or run before it is known to work.
	if d.sandboxErr != nil {
		ended(unsandboxed(d.sandboxErr))
		return nil
	}
	if d.bases == nil {
		ended(errors.New("this machine has no language bases"))
		return nil
	}
	// The environment is the rerun's own, beside its scripts and gone with
	// them.
	study := envs.ForStudy(d.bases, filepath.Join(dir, rerunEnv))
	study.PythonVersion = c.Rerun.Python
	if err := os.Mkdir(study.Dir, 0o700); err != nil {
		ended(err)
		return nil
	}
	private, err := d.privatePaths()
	if err != nil {
		ended(err)
		return nil
	}
	opts := agent.ExecOptions{Dir: dir}
	door := &Door{Guest: guest, Server: d.cfg.Server, Study: study, PrivatePaths: private, PrivateRoots: []string{d.cfg.Home},
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
		ended(err)
		return nil
	}
	defer door.Close()
	// The rerun keeps the study's Python as given; the server already has it.
	door.Study.PersistPython = nil
	if _, err := door.command(runCtx, "/usr/bin/true"); err != nil {
		ended(err)
		return nil
	}
	if err := d.rebuild(runCtx, door, c.Rerun); err != nil {
		ended(err)
		return nil
	}
	// The scripts ran beside Experimentation's services, which a rerun
	// starts the same way.
	exp := c
	exp.AgentRun.BoardColumn = "experimentation"
	services, err := d.startServices(runCtx, exp, door.Study, guest)
	if err != nil {
		ended(err)
		return nil
	}
	defer services.Stop()
	door.ServiceEnv = services.Env()
	root, err := os.OpenRoot(dir)
	if err != nil {
		ended(err)
		return nil
	}
	defer root.Close()
	// ponytail: each step gets an even share of the body limit, cut from its
	// tail; share by need if one step's output crowds out the others.
	share := maxRunBody / max(len(c.Rerun.Steps), 1)
	for _, s := range c.Rerun.Steps {
		// A step is written just before it runs: a script may have changed
		// between two runs of the same path.
		err := protectedStep(s.Path)
		if err == nil {
			err = root.MkdirAll(filepath.Dir(s.Path), 0o700)
		}
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
	// An earlier attempt's outputs are kept as it saved them.
	saved, sums := map[string]bool{}, map[string]string{}
	for _, o := range c.Rerun.Saved {
		saved[o.Name], sums[o.Name] = true, o.Sha256
	}
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

// protectedStep refuses a script path the rerun keeps for itself: its
// environment and what the run folder holds for the CLIs and the daemon.
func protectedStep(p string) error {
	clean := path.Clean(filepath.ToSlash(p))
	names := []string{rerunEnv}
	for _, f := range agent.RunFolderConfig {
		names = append(names, f.Name)
	}
	for _, name := range names {
		if strings.EqualFold(clean, name) || len(clean) > len(name) && strings.EqualFold(clean[:len(name)+1], name+"/") {
			return fmt.Errorf("%s is kept for the run", name)
		}
	}
	return nil
}

// rebuild makes the rerun's fresh environment, for each language its steps
// use, from that language's newest step's saved package list, which holds
// its base and what the study added.
//   - Python: a conda environment from the saved lock, then the study's own
//     packages at the versions pip froze.
//   - R: the machine's base, which must be the one saved, and each package
//     the study installed at its saved version from CRAN.
//   - Julia: the machine's base, which must be the one saved, and the
//     study's saved Manifest.toml, instantiated.
func (d *daemon) rebuild(ctx context.Context, door *Door, rerun *service.ClaimedRerun) error {
	lists := map[string]string{}
	var langs []string
	for _, s := range rerun.Steps {
		if _, ok := lists[s.Language]; !ok {
			langs = append(langs, s.Language)
		}
		lists[s.Language] = s.Packages
	}
	limit := d.rebuildLimit
	if limit == 0 {
		limit = envs.SetupLimit()
	}
	for _, lang := range langs {
		saved, base, own, ok := savedPackages(lists[lang])
		if !ok || saved != lang {
			return unrebuildable{fmt.Errorf("no package list was saved for %s", lang)}
		}
		// Each language's setup has the setup limit; running past it is a
		// timeout, never the saved list's fault.
		setupCtx, stop := context.WithTimeout(ctx, limit)
		start := time.Now()
		err := d.rebuildLanguage(setupCtx, door, rerun.Python, lang, base, own)
		stop()
		if err != nil && (setupCtx.Err() == context.DeadlineExceeded || (ctx.Err() == nil && time.Since(start) >= limit)) {
			return fmt.Errorf("%w: rebuilding %s: %v", context.DeadlineExceeded, lang, err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// networkTrouble is output that says a download failed for the network's
// sake; refusal is output that says the saved list asks for what can't be had.
var (
	networkTrouble = regexp.MustCompile(`(?i)could not resolve host|temporary failure in name resolution|name or service not known|connection (refused|reset|timed out|aborted)|network is unreachable|\btimed out\b|max retries exceeded|newconnectionerror|connecttimeouterror|readtimeouterror|\bssl: |sslerror|proxyerror|tls handshake`)
	refusal        = regexp.MustCompile(`(?i)could not solve|nothing provides|unsatisfiable|is not installable|no matching distribution|could not find a version|resolutionimpossible|\b404\b|does not exist|the lock must provide|the lock's python must include|python must be a version|cannot be found|could not find package|expected package`)
)

// judged makes a failed setup step's error unrebuildable when its output
// says the saved list asks for what can't be had, unless the network failed.
func judged(err error, output string) error {
	text := err.Error() + "\n" + output
	if !networkTrouble.MatchString(text) && refusal.MatchString(text) {
		return unrebuildable{fmt.Errorf("%w: %s", err, tail(output))}
	}
	return fmt.Errorf("%w: %s", err, tail(output))
}

func tail(s string) string {
	if len(s) > 4096 {
		return s[len(s)-4096:]
	}
	return s
}

func (d *daemon) rebuildLanguage(ctx context.Context, door *Door, python, lang, base, own string) error {
	study := door.Study
	switch lang {
	case envs.Python:
		if saved := lockPlatform(base); saved != "" {
			if here, err := envs.CondaPlatform(); err == nil && here != saved {
				return otherMachine{fmt.Errorf("the study's Python environment was saved on %s, and this machine is %s", saved, here)}
			}
		}
		lock := filepath.Join(study.Dir, "saved-lock.txt")
		if err := os.WriteFile(lock, []byte(base), 0o600); err != nil {
			return err
		}
		setUp := d.setUpConda
		if setUp == nil {
			setUp = (*envs.Study).SetUpConda
		}
		if out, err := setUp(study, ctx, python, nil, lock); err != nil {
			return judged(err, out)
		}
		if strings.TrimSpace(own) == "" {
			return nil
		}
		reqs := filepath.Join(study.Dir, "saved-requirements.txt")
		if err := os.WriteFile(reqs, []byte(own), 0o600); err != nil {
			return err
		}
		interpreter, _, err := study.Interpreter(envs.Python)
		if err != nil {
			return err
		}
		env, err := study.Environment(ctx)
		if err != nil {
			return err
		}
		if out, err := door.runSandboxed(ctx, env, study.Dir, interpreter, "-m", "pip", "install", "--no-deps", "--no-input", "--disable-pip-version-check", "-r", reqs); err != nil {
			return judged(err, string(out))
		}
		return nil
	case envs.R, envs.Julia:
		if err := study.Bases.Ensure(ctx, lang); err != nil {
			return err
		}
		fresh, err := study.Packages(ctx, lang)
		if err != nil {
			return err
		}
		if _, now, _, _ := savedPackages(fresh); now != base {
			return otherMachine{fmt.Errorf("this machine's %s base is not the one the study ran on", lang)}
		}
		if strings.TrimSpace(own) == "" {
			return nil
		}
		env, err := study.Environment(ctx)
		if err != nil {
			return err
		}
		interpreter, _, err := study.Interpreter(lang)
		if err != nil {
			return err
		}
		if lang == envs.R {
			return d.rebuildR(ctx, door, env, interpreter, own)
		}
		project := filepath.Join(study.Dir, "julia")
		if err := os.WriteFile(filepath.Join(project, "Manifest.toml"), []byte(own), 0o600); err != nil {
			return err
		}
		if out, err := door.runSandboxed(ctx, env, project, interpreter, "-e", instantiateManifest); err != nil {
			return judged(err, string(out))
		}
		return nil
	}
	return unrebuildable{fmt.Errorf("unknown language %q", lang)}
}

// lockPlatform is the conda platform a saved lock names, "" when it names none.
func lockPlatform(lock string) string {
	for line := range strings.Lines(lock) {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "# platform:"); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// rPackage and rVersion are what a saved R package's name and version may
// be; the list was written by the study's code, so nothing else reaches a URL.
var (
	rPackage = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.]*[A-Za-z0-9]$`)
	rVersion = regexp.MustCompile(`^[0-9]+([.-][0-9]+)+$`)
)

// rebuildR fetches each package the study installed, as the saved list
// names it, at its saved version from CRAN: the current source if that is
// the version, else the archived one. The daemon fetches; the packages are
// built and installed into the fresh library inside the sandbox, with no
// network needed there.
func (d *daemon) rebuildR(ctx context.Context, door *Door, env []string, rscript, own string) error {
	dir := filepath.Join(door.Study.Dir, "saved-r")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	var tarballs []string
	versions := map[string]string{}
	for line := range strings.Lines(own) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !rPackage.MatchString(f[0]) || !rVersion.MatchString(f[1]) {
			return unrebuildable{fmt.Errorf("the saved R package list has an unreadable line %q", strings.TrimSpace(line))}
		}
		if v, seen := versions[f[0]]; seen {
			if v != f[1] {
				return unrebuildable{fmt.Errorf("the saved R package list names %s at both %s and %s", f[0], v, f[1])}
			}
			continue
		}
		versions[f[0]] = f[1]
		file, err := fetchCRAN(ctx, dir, f[0], f[1])
		if err != nil {
			return err
		}
		tarballs = append(tarballs, file)
	}
	out, err := door.runSandboxed(ctx, env, door.Study.Dir, rscript, append([]string{"-e", installSaved}, tarballs...)...)
	// The sources are all here: an install that ran and exited saying a
	// package won't build from them can't be rebuilt on this machine. One
	// that couldn't start, or was killed, is the machine's.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return unrebuildable{fmt.Errorf("%w: %s", err, tail(string(out)))}
	}
	return err
}

func fetchCRAN(ctx context.Context, dir, pkg, version string) (string, error) {
	file := pkg + "_" + version + ".tar.gz"
	for _, u := range []string{cranURL + "/src/contrib/" + file, cranURL + "/src/contrib/Archive/" + pkg + "/" + file} {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return "", err
		}
		resp, err := cranClient.Do(req)
		if err != nil {
			return "", err
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", fmt.Errorf("fetching %s: %s", u, resp.Status)
		}
		if resp.ContentLength > maxCRANBytes {
			resp.Body.Close()
			return "", unrebuildable{fmt.Errorf("%s is over %d MiB", file, maxCRANBytes>>20)}
		}
		path := filepath.Join(dir, file)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		var n int64
		if err == nil {
			n, err = io.Copy(f, io.LimitReader(resp.Body, maxCRANBytes+1))
			err = errors.Join(err, f.Close())
		}
		resp.Body.Close()
		if err == nil && n > maxCRANBytes {
			return "", unrebuildable{fmt.Errorf("%s is over %d MiB", file, maxCRANBytes>>20)}
		}
		return path, err
	}
	return "", unrebuildable{fmt.Errorf("CRAN has no %s %s", pkg, version)}
}

// installSaved installs the source packages it is given into the study's
// library, passing over the list until each is in or none more goes in, so
// a package can follow the ones it needs.
const installSaved = `lib <- Sys.getenv("R_LIBS_USER")
left <- commandArgs(TRUE)
name <- function(f) sub("_[^_]*$", "", basename(f))
repeat {
  before <- length(left)
  for (f in left) {
    suppressWarnings(install.packages(f, repos = NULL, type = "source", lib = lib))
    if (name(f) %in% rownames(installed.packages(lib.loc = lib))) left <- setdiff(left, f)
  }
  if (!length(left)) quit(status = 0)
  if (length(left) == before) { cat("not installed:", basename(left), "\n"); quit(status = 1) }
}`

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
