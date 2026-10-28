package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/daemon/sandbox"
)

func TestBuildConfinement(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	if _, err := exec.LookPath("latexmk"); err != nil {
		t.Fatal("real TeX required: ", err)
	}
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"normal", `Hello world.`, true},
		{"write18", `\immediate\write18{touch ../escaped}`, false},
		{"passwd", `\input{/etc/passwd}`, false},
		{"openout", `\newwrite\f\immediate\openout\f=../escaped\immediate\write\f{x}`, false},
		{"network", `\input{|curl https://example.com}`, false},
		{"loop", `\def\x{\x}\x`, false},
		{"memory", `\def\x{x}\loop\edef\x{\x\x}\iftrue\repeat`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "src")
			tmp := filepath.Join(root, "tmp")
			os.Mkdir(dir, 0700)
			os.Mkdir(tmp, 0700)
			os.WriteFile(filepath.Join(dir, "main.tex"), []byte("\\documentclass{article}\n\\begin{document}\n"+tc.body+"\n\\end{document}\n"), 0600)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			result := buildTestDaemon(t).compileBuild(ctx, dir, tmp, BuildMemoryCap)
			if (result.Status == "ok") != tc.ok {
				t.Fatalf("status=%s reason=%s log=%s", result.Status, result.Reason, result.Log)
			}
			if tc.ok {
				for _, p := range []string{"main.pdf", "main.synctex.gz"} {
					if fi, e := os.Stat(filepath.Join(dir, p)); e != nil || fi.Size() == 0 {
						t.Fatalf("missing %s: %v", p, e)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
				t.Fatal("escaped write", err)
			}
		})
	}
}
func TestBuildIgnoresLatexmkRC(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "src")
	tmp := filepath.Join(root, "tmp")
	os.Mkdir(dir, 0700)
	os.Mkdir(tmp, 0700)
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte(`\documentclass{article}\begin{document}Safe\end{document}`), 0600)
	os.WriteFile(filepath.Join(dir, ".latexmkrc"), []byte(`die "HOSTILE RC EXECUTED";`), 0600)
	result := buildTestDaemon(t).compileBuild(t.Context(), dir, tmp, BuildMemoryCap)
	if result.Status != "ok" || strings.Contains(result.Log, "HOSTILE RC EXECUTED") {
		t.Fatalf("%+v", result)
	}
}

func TestBuildMemoryCap(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	dir := t.TempDir()
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte(`\documentclass{article}\begin{document}\def\x{\x}\x\end{document}`), 0600)
	result := buildTestDaemon(t).compileBuild(t.Context(), dir, tmp, 1)
	if result.Status != "failed" || result.Reason != "memory" {
		t.Fatalf("cap not enforced: %+v", result)
	}
}
func TestBuildBiber(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	if _, e := exec.LookPath("biber"); e != nil {
		t.Fatal("biber is required for this acceptance test")
	}
	dir := t.TempDir()
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte(`\documentclass{article}\usepackage[backend=biber]{biblatex}\addbibresource{refs.bib}\begin{document}\cite{sample}\printbibliography\end{document}`), 0600)
	os.WriteFile(filepath.Join(dir, "refs.bib"), []byte(`@book{sample,author={A. Author},title={Example},year={2025}}`), 0600)
	result := buildTestDaemon(t).compileBuild(t.Context(), dir, tmp, BuildMemoryCap)
	if result.Status != "ok" {
		t.Fatalf("%+v", result)
	}
	b, e := os.ReadFile(filepath.Join(dir, "main.bbl"))
	if e != nil || !strings.Contains(string(b), "Example") {
		t.Fatal("missing bibliography", e)
	}
}

func TestBuildDispatchPublishesArtifacts(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	f := newCoreFixture(t)
	f.saved("main.tex", `\documentclass{article}\begin{document}Build output\end{document}`, 1)
	var status string
	var pdf, synctex int64
	original := http.DefaultTransport
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/builds/") && strings.HasSuffix(r.URL.Path, "/finish") {
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				return nil, e
			}
			defer r.MultipartForm.RemoveAll()
			var m struct{ Status string }
			json.Unmarshal([]byte(r.FormValue("meta")), &m)
			status = m.Status
			for name, files := range r.MultipartForm.File {
				for _, file := range files {
					if name == "pdf" {
						pdf = file.Size
					}
					if name == "synctex" {
						synctex = file.Size
					}
				}
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return original.RoundTrip(r)
	})}
	b := coresearch.ClaimedBuild{WorkContext: f.work, ID: uuid.New(), Seq: 1, Sources: f.files}
	b.ChatID = uuid.Nil
	if e := f.d.runBuild(t.Context(), b.WorkContext, b); e != nil {
		t.Fatal(e)
	}
	if status != "ok" || pdf == 0 || synctex == 0 {
		t.Fatal(status, pdf, synctex)
	}
	if _, e := os.Stat(filepath.Join(f.d.cfg.Home, "coresearch-builds", b.ID.String())); !os.IsNotExist(e) {
		t.Fatal("build folder retained", e)
	}
}

func TestBuildBiberNoNetwork(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	if _, e := exec.LookPath("biber"); e != nil {
		t.Fatal("biber is required for this acceptance test")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `@book{sample,title={Network},year={2025}}`)
	}))
	defer srv.Close()
	dir, tmp := t.TempDir(), t.TempDir()
	source := `\documentclass{article}\usepackage[backend=biber]{biblatex}\addbibresource[location=remote]{` + srv.URL + `/refs.bib}\begin{document}\cite{sample}\printbibliography\end{document}`
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte(source), 0600)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result := buildTestDaemon(t).compileBuild(ctx, dir, tmp, BuildMemoryCap)
	if result.Status != "failed" || hits.Load() != 0 {
		t.Fatalf("network result %+v hits %d", result, hits.Load())
	}
}

func TestBuildCannotPublishPlantedArtifacts(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	dir, tmp := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte(`\documentclass{article}\stop`), 0600)
	for _, p := range []string{"main.pdf", "main.synctex.gz"} {
		os.WriteFile(filepath.Join(dir, p), []byte("planted"), 0600)
	}
	result := buildTestDaemon(t).compileBuild(t.Context(), dir, tmp, BuildMemoryCap)
	if result.Status == "ok" {
		t.Fatalf("planted artifacts published %+v", result)
	}
}

func TestBuildOversizedLogFails(t *testing.T) {
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	dir, tmp := t.TempDir(), t.TempDir()
	source := `\documentclass{article}\begin{document}Text\newcount\n\n=0\loop\typeout{` + strings.Repeat("x", 1024) + `}\advance\n by 1\ifnum\n<2200\repeat\end{document}`
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte(source), 0600)
	result := buildTestDaemon(t).compileBuild(t.Context(), dir, tmp, BuildMemoryCap)
	if result.Status == "ok" {
		t.Fatal("oversized uninspected log accepted")
	}
}

func TestBuildReceiptRecovery(t *testing.T) {
	f := newCoreFixture(t)
	w := f.work
	w.ChatID = uuid.Nil
	r := coreReceipt{Work: w, Kind: "builds", ID: uuid.New()}
	must(t, f.d.journalCoresearch(w, r.Kind, r.ID))
	scratch := buildScratchFixture(t, f.d, r)
	unrelated := filepath.Join(f.d.cfg.Home, "coresearch-builds", uuid.NewString())
	must(t, os.Mkdir(unrelated, 0700))
	must(t, f.d.recoverCoresearch())
	if _, e := os.Stat(scratch); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("recovered build scratch retained", e)
	}
	if _, e := os.Stat(unrelated); e != nil {
		t.Fatal("unrelated scratch removed", e)
	}
	recovered, e := readCoreReceipt(filepath.Join(f.d.coreReceiptDir(r), "finish.json"))
	must(t, e)
	var meta map[string]any
	json.Unmarshal(recovered.Body, &meta)
	if meta["status"] != "failed" {
		t.Fatal(meta)
	}
	must(t, f.d.flushCoresearch(t.Context()))
	if f.finishes.Load() != 1 {
		t.Fatal("build receipt not delivered")
	}
}
func TestBuildMissingTexFailsClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result := buildTestDaemon(t).compileBuild(t.Context(), t.TempDir(), t.TempDir(), BuildMemoryCap)
	if result.Status != "failed" || result.Reason != buildInstallMessage {
		t.Fatal(result)
	}
}

// These files can live inside an otherwise approved installation tree when a
// daemon uses custom state or LYK_CONFIG. The TeX grant must not reveal them.
func TestBuildPrivateRuntimePaths(t *testing.T) {
	tex, dir, tmp := t.TempDir(), t.TempDir(), t.TempDir()
	state := filepath.Join(tex, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(tex, "lyk-config.json")
	t.Setenv("LYK_CONFIG", secret)
	for _, path := range []string{secret, filepath.Join(state, "token"), filepath.Join(tex, "public.tex")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := buildTestDaemon(t)
	d.cfg.StateDir = state
	p, err := d.buildPolicy(dir, tmp, []string{tex})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		allowed bool
	}{{secret, false}, {filepath.Join(state, "token"), false}, {filepath.Join(tex, "public.tex"), true}} {
		cmd, err := p.Command(t.Context(), "/bin/cat", tc.path)
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		sandbox.CloseCommand(cmd)
		if tc.allowed && (err != nil || string(out) != "fixture") {
			t.Errorf("public TeX file unreadable: %q %v", out, err)
		} else if !tc.allowed && strings.Contains(string(out), "fixture") {
			t.Errorf("private runtime data readable: %s", tc.path)
		}
	}
}

func buildTestDaemon(t *testing.T) *daemon {
	t.Helper()
	return &daemon{cfg: Config{Home: t.TempDir()}}
}

func buildScratchFixture(t *testing.T, d *daemon, r coreReceipt) string {
	t.Helper()
	root := filepath.Join(d.cfg.Home, "coresearch-builds", r.ID.String())
	must(t, os.MkdirAll(filepath.Join(root, "src"), 0700))
	must(t, atomicJSON(root, "owner.json", coreReceipt{Work: r.Work, Kind: r.Kind, ID: r.ID}))
	must(t, os.WriteFile(filepath.Join(root, "src", "main.tex"), []byte("unfinished source"), 0600))
	must(t, os.WriteFile(filepath.Join(root, "output-partial"), []byte("unfinished spool"), 0600))
	return root
}
func TestBuildRecoveryPreservesDurableOutputs(t *testing.T) {
	f := newCoreFixture(t)
	r := coreReceipt{Work: f.work, Kind: "builds", ID: uuid.New(), Body: json.RawMessage(`{"status":"ok","pages":1}`)}
	must(t, f.d.journalCoresearch(r.Work, r.Kind, r.ID))
	scratch := buildScratchFixture(t, f.d, r)
	data := []byte("immutable output")
	output := filepath.Join(f.d.coreReceiptDir(r), "output-00000000")
	must(t, os.WriteFile(output, data, 0600))
	r.Outputs = []coreOutputReceipt{{Path: "pdf", File: "output-00000000", Size: int64(len(data)), SHA: fmt.Sprintf("%x", sha256.Sum256(data))}}
	must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
	must(t, f.d.recoverCoresearch())
	if _, e := os.Stat(scratch); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("completed scratch retained", e)
	}
	got, e := os.ReadFile(output)
	must(t, e)
	if string(got) != string(data) {
		t.Fatal("pending durable output removed")
	}
}
func TestBuildRecoveryValidatesScratchOwnership(t *testing.T) {
	for _, scenario := range []string{"mismatched-owner", "scratch-link", "missing-durable-output", "invalid-finish"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCoreFixture(t)
			r := coreReceipt{Work: f.work, Kind: "builds", ID: uuid.New()}
			must(t, f.d.journalCoresearch(r.Work, r.Kind, r.ID))
			scratch := buildScratchFixture(t, f.d, r)
			protected := scratch
			switch scenario {
			case "mismatched-owner":
				other := r
				other.Work.ClaimToken = uuid.New()
				must(t, atomicJSON(scratch, "owner.json", other))
			case "scratch-link":
				protected = filepath.Join(t.TempDir(), "protected")
				must(t, os.Rename(scratch, protected))
				must(t, os.Symlink(protected, scratch))
			case "missing-durable-output":
				r.Body = json.RawMessage(`{"status":"ok"}`)
				r.Outputs = []coreOutputReceipt{{Path: "pdf", File: "output-00000000", Size: 1, SHA: fmt.Sprintf("%x", sha256.Sum256([]byte("x")))}}
				must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
			case "invalid-finish":
				must(t, os.WriteFile(filepath.Join(f.d.coreReceiptDir(r), "finish.json"), []byte("invalid"), 0600))
			}
			if e := f.d.recoverCoresearch(); e == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if _, e := os.Stat(filepath.Join(protected, "src", "main.tex")); e != nil {
				t.Fatal("unsecured evidence removed", e)
			}
		})
	}
}

func TestBuildSpoolsStayOwnedOnReceiptFailure(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	f := newCoreFixture(t)
	f.saved("main.tex", `\documentclass{article}\begin{document}Owned output\end{document}`, 1)
	b := coresearch.ClaimedBuild{WorkContext: f.work, ID: uuid.New(), Seq: 1, Sources: f.files}
	b.ChatID = uuid.Nil
	receipt := coreReceipt{Work: b.WorkContext, Kind: "builds", ID: b.ID}
	original := http.DefaultTransport
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			if e := os.WriteFile(filepath.Join(f.d.coreReceiptDir(receipt), "finish.json"), []byte("broken publication"), 0600); e != nil {
				return nil, e
			}
		}
		return original.RoundTrip(r)
	})}
	if e := f.d.runBuild(t.Context(), b.WorkContext, b); e == nil {
		t.Fatal("expected receipt publication failure")
	}
	scratch := filepath.Join(f.d.cfg.Home, "coresearch-builds", b.ID.String())
	spools, e := filepath.Glob(filepath.Join(scratch, "output-*"))
	must(t, e)
	if len(spools) != 3 {
		t.Fatalf("outputs escaped owned scratch or were removed before durable receipt: %v", spools)
	}
	if e := f.d.recoverCoresearch(); e == nil {
		t.Fatal("invalid durable evidence accepted")
	}
	for _, p := range spools {
		if _, e := os.Stat(p); e != nil {
			t.Fatal("pending spool removed", e)
		}
	}
	must(t, os.Remove(filepath.Join(f.d.coreReceiptDir(receipt), "finish.json")))
	must(t, f.d.recoverCoresearch())
	if _, e := os.Stat(scratch); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("scratch survived durable failure recovery", e)
	}
	r, e := readCoreReceipt(filepath.Join(f.d.coreReceiptDir(receipt), "finish.json"))
	must(t, e)
	if !strings.Contains(string(r.Body), `"status":"failed"`) {
		t.Fatal(string(r.Body))
	}
}

func TestBuildRecoveryResumesScratchDeletion(t *testing.T) {
	f := newCoreFixture(t)
	r := coreReceipt{Work: f.work, Kind: "builds", ID: uuid.New(), Body: json.RawMessage(`{"status":"failed"}`)}
	must(t, f.d.journalCoresearch(r.Work, r.Kind, r.ID))
	root := buildScratchFixture(t, f.d, r)
	must(t, atomicJSON(f.d.coreReceiptDir(r), "finish.json", r))
	tombstone := filepath.Join(filepath.Dir(root), ".gc-"+r.ID.String()+"-"+r.Work.ClaimToken.String())
	must(t, os.Rename(root, tombstone))
	// Deletion may already have removed the marker before the daemon crashed.
	must(t, os.Remove(filepath.Join(tombstone, "owner.json")))
	must(t, f.d.recoverCoresearch())
	if _, e := os.Stat(tombstone); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("interrupted scratch deletion not resumed", e)
	}
}
