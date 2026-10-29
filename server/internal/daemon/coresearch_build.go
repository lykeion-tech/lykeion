package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/daemon/sandbox"
)

const BuildLimit = 2 * time.Minute
const BuildMemoryCap int64 = 2 << 30
const buildInstallMessage = "Install TeX Live (or MacTeX) to build papers."
const buildMonitorInterval = 100 * time.Millisecond

type buildResult struct {
	Status, Reason, Log string
	Pages               int
}

func buildTools(ctx context.Context) (string, []string, error) {
	latex, err := exec.LookPath("latexmk")
	if err != nil {
		return "", nil, err
	}
	latex, err = filepath.EvalSymlinks(latex)
	if err != nil {
		return "", nil, err
	}
	kpse, err := exec.LookPath("kpsewhich")
	if err != nil {
		return "", nil, err
	}
	roots := []string{}
	for _, name := range []string{"TEXMFROOT", "TEXMFSYSVAR"} {
		cmd := exec.CommandContext(ctx, kpse, "-var-value="+name)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent"}
		data, e := cmd.Output()
		if e != nil {
			return "", nil, e
		}
		root, e := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
		if e != nil || !filepath.IsAbs(root) || root == "/" {
			return "", nil, fmt.Errorf("invalid TeX system root %s", name)
		}
		if !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	return latex, roots, nil
}

func (d *daemon) buildPolicy(dir, tmp string, texRoots []string) (sandbox.Policy, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sandbox.Policy{}, err
	}
	private, err := d.privatePaths()
	if err != nil {
		return sandbox.Policy{}, err
	}
	roots := []string{d.cfg.Home}
	if d.bases != nil {
		roots = append(roots, d.bases.Dir)
	}
	for _, root := range roots {
		if err := sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return sandbox.Policy{}, err
		}
	}
	return sandbox.Policy{StrictRead: true, ConfineEtc: true, NoNetwork: true, Home: home, Write: []string{dir, tmp}, Read: texRoots, Hidden: private, RestrictedRoots: roots}, nil
}
func (d *daemon) compileBuild(parent context.Context, dir, tmp string, capBytes int64) buildResult {
	result := buildResult{Status: "failed"}
	ctx, cancel := context.WithTimeout(parent, BuildLimit)
	defer cancel()
	latex, root, err := buildTools(ctx)
	if err != nil {
		result.Reason = buildInstallMessage
		return result
	}
	policy, err := d.buildPolicy(dir, tmp, root)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	// The source tree never supplies latexmk's executable rule database. Even
	// without rc files, .fdb_latexmk restores rule inputs and output paths.
	err = filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source links are forbidden")
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".fdb_latexmk") {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	cmd, err := policy.Command(ctx, latex, "-norc", "-pdf", "-interaction=nonstopmode", "-halt-on-error", "-no-shell-escape", "-synctex=1", "main.tex")
	if err != nil {
		result.Reason = buildInstallMessage + " " + err.Error()
		return result
	}
	defer sandbox.CloseCommand(cmd)
	home := filepath.Join(tmp, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		result.Reason = err.Error()
		return result
	}
	// Resolve binaries through trusted installation directories only. User TeX,
	// Perl, loader and shell configuration are deliberately not inherited.
	cmd.Env = []string{"PATH=" + filepath.Join(root[0], "bin", "universal-darwin") + ":" + filepath.Dir(latex) + ":/opt/homebrew/bin:/Library/TeX/texbin:/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "LANG=C", "openin_any=p", "openout_any=p", "TEXMFHOME=" + home, "TEXMFVAR=" + tmp, "TEXMFCONFIG=" + home, "shell_escape=f", "VARTEXFONTS=" + filepath.Join(tmp, "fonts")}
	cmd.Dir = dir
	log := &recordedTail{limit: 2 << 20}
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = time.Second
	if err = cmd.Start(); err != nil {
		result.Reason = buildInstallMessage + " " + err.Error()
		return result
	}
	defer killGroup(cmd)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(buildMonitorInterval)
	defer ticker.Stop()
	waiting := true
	for waiting {
		select {
		case err = <-done:
			waiting = false
		case <-ticker.C:
			sampleCtx, stop := context.WithTimeout(context.Background(), time.Second)
			s, e := sampleProcessTree(sampleCtx, cmd.Process.Pid)
			stop()
			if e != nil {
				result.Reason = "process monitoring failed"
				cancel()
			} else if s.RSSBytes > capBytes {
				result.Reason = "memory"
				cancel()
			}
		}
	}
	result.Log = log.String()
	if result.Reason != "" {
		return result
	}
	if ctx.Err() != nil {
		result.Reason = "timeout"
		return result
	}
	if err != nil {
		result.Reason = "latexmk failed"
		return result
	}
	texlog, e := readBounded(filepath.Join(dir, "main.log"), 2<<20)
	if e != nil {
		result.Reason = "build log cannot be validated: " + e.Error()
		return result
	}
	result.Log += "\n" + string(texlog)
	if strings.Contains(string(texlog), "runsystem(") {
		result.Reason = "shell command refused"
		return result
	}
	for _, name := range []string{"main.pdf", "main.synctex.gz"} {
		fi, e := os.Lstat(filepath.Join(dir, name))
		if e != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			result.Reason = "missing build output"
			return result
		}
	}
	if match := regexp.MustCompile(`Output written on .*\(([0-9]+) pages?`).FindStringSubmatch(string(texlog)); len(match) > 1 {
		result.Pages, _ = strconv.Atoi(match[1])
	}
	result.Status = "ok"
	return result
}

func (d *daemon) runBuild(parent context.Context, w coresearch.WorkContext, b coresearch.ClaimedBuild) (resultErr error) {
	if err := d.journalCoresearch(w, "builds", b.ID); err != nil {
		return err
	}
	var outputs []recordedOutput
	durable := false
	var root string
	meta := map[string]any{"status": "failed", "pages": 0}
	defer func() {
		if durable {
			closeRecordedOutputs(outputs)
		}
	}()
	defer func() {
		if resultErr != nil {
			meta["status"] = "failed"
			meta["reason"] = resultErr.Error()
		}
		var e error
		durable, e = d.queueCoresearch(w, "builds", b.ID, meta, outputs)
		resultErr = errors.Join(resultErr, e)
	}()
	if d.sandboxErr != nil {
		return fmt.Errorf("%s %w", buildInstallMessage, d.sandboxErr)
	}
	if err := d.coreAPI(w.WorkspaceID).Do(parent, "POST", d.coreWorkPath(w, "builds/"+b.ID.String()+"/start"), nil, nil); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, BuildLimit)
	defer cancel()
	watched := make(chan struct{})
	go func() { defer close(watched); d.watchCoresearch(ctx, cancel, w, "builds", b.ID) }()
	defer func() { cancel(); <-watched }()
	owner := coreReceipt{Work: w, Kind: "builds", ID: b.ID}
	var err error
	root, err = d.createBuildScratch(owner)
	if err != nil {
		return err
	}
	dir, tmp := filepath.Join(root, "src"), filepath.Join(root, "tmp")
	for _, p := range []string{dir, tmp} {
		if e := os.Mkdir(p, 0700); e != nil {
			return e
		}
	}
	seen := map[string]bool{}
	var total int64
	for _, ref := range b.Sources {
		path, e := coresearch.CleanPath(ref.Path)
		if e != nil || path != ref.Path || seen[path] || ref.Size < 0 || ref.Size > coresearch.MaxFileBytes || ref.N <= 0 {
			return errors.New("invalid build source")
		}
		seen[path] = true
		total += ref.Size
		if total > coresearch.MaxProjectBytes {
			return errors.New("build sources exceed project cap")
		}
		hash, e := hex.DecodeString(ref.SHA256)
		if e != nil || len(hash) != 32 {
			return errors.New("invalid source digest")
		}
		dest := filepath.Join(dir, path)
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		sum := sha256.New()
		bounded := &fillWriter{w: io.MultiWriter(f, sum), left: ref.Size}
		e = d.coreAPI(w.WorkspaceID).Download(ctx, d.corePath("blobs/"+ref.SHA256), bounded)
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		if bounded.left != 0 || hex.EncodeToString(sum.Sum(nil)) != ref.SHA256 {
			return errors.New("source digest or size mismatch")
		}
	}
	result := d.compileBuild(ctx, dir, tmp, BuildMemoryCap)
	meta["status"] = result.Status
	meta["pages"] = result.Pages
	meta["reason"] = result.Reason
	logPath := filepath.Join(root, "build.log")
	if e := os.WriteFile(logPath, []byte(result.Log), 0600); e != nil {
		return e
	}
	artifacts := map[string]string{"log": logPath}
	if result.Status == "ok" {
		artifacts["pdf"] = filepath.Join(dir, "main.pdf")
		artifacts["synctex"] = filepath.Join(dir, "main.synctex.gz")
	}
	for _, name := range []string{"pdf", "log", "synctex"} {
		path, ok := artifacts[name]
		if !ok {
			continue
		}
		f, e := os.OpenFile(path, os.O_RDONLY|scriptOpen, 0)
		if e != nil {
			return e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > coresearch.MaxFileBytes {
			f.Close()
			return errors.New("invalid build output")
		}
		spool, e := os.CreateTemp(root, "output-")
		if e != nil {
			f.Close()
			return e
		}
		sum := sha256.New()
		size, e := io.Copy(io.MultiWriter(spool, sum), io.LimitReader(f, coresearch.MaxFileBytes+1))
		e = errors.Join(e, f.Close(), spool.Sync(), spool.Close(), syncDir(root))
		if e != nil || size > coresearch.MaxFileBytes {
			os.Remove(spool.Name())
			return errors.New("build output exceeds limit")
		}
		outputs = append(outputs, recordedOutput{path: name, sha: hex.EncodeToString(sum.Sum(nil)), file: spool.Name(), size: size})
	}
	return nil
}
func (d *daemon) finishBuild(ctx context.Context, r coreReceipt, outputs []recordedOutput) error {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		e := form.WriteField("meta", string(r.Body))
		for _, out := range outputs {
			if e != nil {
				break
			}
			var part io.Writer
			part, e = form.CreateFormFile(out.path, out.path)
			if e == nil {
				var f *os.File
				f, e = coresearch.OpenSnapshot(out.file, out.size, out.sha)
				if e == nil {
					_, e = io.Copy(part, f)
					e = errors.Join(e, f.Close())
				}
			}
		}
		if e == nil {
			e = form.Close()
		}
		writer.CloseWithError(e)
		done <- e
	}()
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(d.api.Server, "/")+d.coreWorkPath(r.Work, "builds/"+r.ID.String()+"/finish"), reader)
	if e != nil {
		reader.CloseWithError(e)
		<-done
		return e
	}
	req.Header.Set("Authorization", "Bearer "+d.api.Token)
	req.Header.Set("X-Workspace-ID", r.Work.WorkspaceID.String())
	req.Header.Set("Content-Type", form.FormDataContentType())
	client := http.Client{Timeout: 5 * time.Minute}
	if d.api.HTTP != nil {
		client = *d.api.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	reader.Close()
	writeErr := <-done
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return &apiclient.HTTPError{Status: resp.StatusCode, Msg: "build finish refused"}
	}
	return writeErr
}

func sameBuildIdentity(a, b coreReceipt) bool {
	return a.Kind == "builds" && b.Kind == "builds" && a.ID == b.ID && a.Work.WorkspaceID == b.Work.WorkspaceID && a.Work.ProjectID == b.Work.ProjectID && a.Work.ChatID == b.Work.ChatID && a.Work.ClaimToken == b.Work.ClaimToken
}

// The exact initialization directory is owned by the durable claim even before
// its marker exists. Only a fully marked tree is published at the final path.
func (d *daemon) createBuildScratch(r coreReceipt) (string, error) {
	claim, err := readCoreReceipt(filepath.Join(d.coreReceiptDir(r), "claim.json"))
	if err != nil {
		return "", err
	}
	if !sameBuildIdentity(claim, r) {
		return "", errors.New("build initialization journal mismatch")
	}
	base := filepath.Join(d.cfg.Home, "coresearch-builds")
	if err := privateDir(base); err != nil {
		return "", err
	}
	stage := filepath.Join(base, ".init-"+r.ID.String()+"-"+r.Work.ClaimToken.String())
	if err := os.Mkdir(stage, 0700); err != nil {
		return "", err
	}
	if err := syncDir(base); err != nil {
		return "", err
	}
	return d.publishBuildScratch(r)
}
func (d *daemon) publishBuildScratch(r coreReceipt) (string, error) {
	claim, err := readCoreReceipt(filepath.Join(d.coreReceiptDir(r), "claim.json"))
	if err != nil {
		return "", err
	}
	if !sameBuildIdentity(claim, r) {
		return "", errors.New("build initialization journal mismatch")
	}
	base := filepath.Join(d.cfg.Home, "coresearch-builds")
	stage := filepath.Join(base, ".init-"+r.ID.String()+"-"+r.Work.ClaimToken.String())
	for _, path := range []string{base, stage} {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("invalid build initialization directory")
		}
	}
	root := filepath.Join(base, r.ID.String())
	if _, err := os.Lstat(root); err == nil {
		return "", os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := atomicJSON(stage, "owner.json", coreReceipt{Work: r.Work, Kind: r.Kind, ID: r.ID}); err != nil {
		return "", err
	}
	if err := os.Rename(stage, root); err != nil {
		return "", err
	}
	return root, syncDir(base)
}

// Only a matching durable journal may authorize reclaiming a build tree. The
// marker and detached spools live above src/tmp, outside the sandbox's writes.
func (d *daemon) cleanupBuildScratch(r coreReceipt, receiptDir string) error {
	if r.Kind != "builds" {
		return nil
	}
	base := filepath.Join(d.cfg.Home, "coresearch-builds")
	info, e := os.Lstat(base)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid build scratch base")
	}
	root := filepath.Join(base, r.ID.String())
	tombstone := filepath.Join(base, ".gc-"+r.ID.String()+"-"+r.Work.ClaimToken.String())
	stage := filepath.Join(base, ".init-"+r.ID.String()+"-"+r.Work.ClaimToken.String())
	exists := map[string]bool{}
	for _, path := range []string{root, tombstone, stage} {
		info, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid build scratch directory")
		}
		exists[path] = true
	}
	if !exists[root] && !exists[tombstone] && !exists[stage] {
		return nil
	}
	if exists[root] {
		owner, e := readCoreReceipt(filepath.Join(root, "owner.json"))
		if errors.Is(e, os.ErrNotExist) {
			// Compatibility with an interrupted pre-staging initialization:
			// an empty final directory contains no unrelated data to erase.
			entries, err := os.ReadDir(root)
			if err != nil {
				return err
			}
			if len(entries) != 0 {
				return errors.New("unowned nonempty build scratch")
			}
		} else if e != nil {
			return e
		} else if !sameBuildIdentity(owner, r) {
			return errors.New("build scratch identity mismatch")
		}
	}
	// Before deleting source/partial spools, verify each pending immutable
	// output named by the already durable terminal receipt is still secured.
	for _, out := range r.Outputs {
		f, e := coresearch.OpenSnapshot(filepath.Join(receiptDir, out.File), out.Size, out.SHA)
		if e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
	}
	// A failed/crashed marker publication leaves only this exact journal-owned
	// staging name. Reclaim it after terminal evidence, without scanning peers.
	if exists[stage] {
		if e = os.RemoveAll(stage); e != nil {
			return e
		}
		if e = syncDir(base); e != nil {
			return e
		}
	}
	// A tombstone's exact build/claim name records prior identity validation.
	// It lives outside sandbox writes, so interrupted RemoveAll can resume even
	// after owner.json has been deleted. No scan of unrelated names is needed.
	if exists[tombstone] {
		if e = os.RemoveAll(tombstone); e != nil {
			return e
		}
		if e = syncDir(base); e != nil {
			return e
		}
	}
	if exists[root] {
		if e = os.Rename(root, tombstone); e != nil {
			return e
		}
		if e = syncDir(base); e != nil {
			return e
		}
		if e = os.RemoveAll(tombstone); e != nil {
			return e
		}
		return syncDir(base)
	}
	return nil
}
