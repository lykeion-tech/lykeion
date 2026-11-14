//go:build linux || darwin

package envs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// realVolumeBases installs with this machine's real volume and sandbox into
// 64 MB images.
func realVolumeBases(t *testing.T) *Bases {
	t.Helper()
	if testing.Short() {
		t.Skip("mounts disk images")
	}
	dir := t.TempDir()
	// Production keeps the bases inside the home folder, which the sandbox
	// hides apart from its grants.
	if parent := os.Getenv("LYK_TEST_ENVS_PARENT"); parent != "" {
		var err error
		if dir, err = os.MkdirTemp(parent, "lyk-envs-"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	b := NewBoundedBases(dir)
	if b.vol == nil {
		t.Skipf("no size-limited volume here: %v", b.volErr)
	}
	t.Cleanup(func() {
		for _, dir := range []string{filepath.Join(b.Dir, "python"), filepath.Join(b.Dir, "r")} {
			if b.vol.mounted(dir) {
				if err := b.vol.detach(context.Background(), dir); err != nil {
					t.Errorf("cleanup detach %s: %v", dir, err)
				}
			}
		}
	})
	return b
}

const testCap = 64 << 20

func TestARealInstallCannotFillMoreThanItsVolume(t *testing.T) {
	b := realVolumeBases(t)
	dir := filepath.Join(b.Dir, "python")
	err := b.provision(context.Background(), "python", dir, filepath.Join(dir, "ready"), testCap, func(ctx context.Context) error {
		_, err := b.Run(ctx, nil, dir, "/bin/dd", "if=/dev/zero", "of=fill", "bs=1048576", "count=256")
		return err
	})
	if err == nil {
		t.Fatal("writing 256 MB into a 64 MB volume succeeded")
	}
	if b.vol.mounted(dir) || b.Ready(Python) {
		t.Fatal("a failed install stayed mounted or ready")
	}
	emptyOrAbsent(t, dir)
	emptyOrAbsent(t, b.images())
}

func TestARealInstallCannotWriteOutsideItsVolume(t *testing.T) {
	b := realVolumeBases(t)
	dir := filepath.Join(b.Dir, "python")
	outside := filepath.Join(t.TempDir(), "outside")
	err := b.provision(context.Background(), "python", dir, filepath.Join(dir, "ready"), testCap, func(ctx context.Context) error {
		_, err := b.Run(ctx, nil, dir, "/bin/sh", "-c", `touch inside && touch "$1"`, "sh", outside)
		return err
	})
	if err == nil {
		t.Fatal("the installer wrote outside its volume")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("file outside the volume: %v", err)
	}
}

func TestARealInstallLeftRunningIsNeverPublished(t *testing.T) {
	b := realVolumeBases(t)
	dir := filepath.Join(b.Dir, "python")
	marker := filepath.Join(dir, "ready")
	pidFile := filepath.Join(t.TempDir(), "pid")
	err := b.provision(context.Background(), "python", dir, marker, testCap, func(ctx context.Context) error {
		// The survivor leaves the installer's session and keeps a file in the
		// volume open; the installer itself exits successfully.
		cmd := exec.Command("/bin/sh", "-c", `exec 3>held; echo $$; exec sleep 60`)
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		buf := make([]byte, 32)
		n, _ := out.Read(buf)
		return os.WriteFile(pidFile, buf[:n], 0o600)
	})
	raw, _ := os.ReadFile(pidFile)
	pid := strings.TrimSpace(string(raw))
	t.Cleanup(func() { exec.Command("kill", "-9", pid).Run() })
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("install with a live writer: %v", err)
	}
	if b.Ready(Python) {
		t.Fatal("a volume still in use counts as ready")
	}
	exec.Command("kill", "-9", pid).Run()
	time.Sleep(500 * time.Millisecond)
	err = b.provision(context.Background(), "python", dir, marker, testCap, func(ctx context.Context) error {
		_, err := b.Run(ctx, nil, dir, "/bin/sh", "-c", `echo base > lock.txt`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Ready(Python) {
		t.Fatal("published base not ready")
	}
	if err := os.WriteFile(filepath.Join(dir, "later"), nil, 0o644); err == nil {
		t.Fatal("a published base is writable")
	}
	entries, _ := os.ReadDir(b.images())
	if len(entries) != 1 || entries[0].Name() != "python"+b.vol.ext() {
		t.Fatalf("images %v", entries)
	}
}

func TestInstallCommandsRunOnlyInsideAnInstall(t *testing.T) {
	if _, err := run(context.Background(), nil, t.TempDir(), "/bin/true"); err == nil {
		t.Fatal("an installer command ran outside an install")
	}
}

func TestInstallCommandsGetHomeAndTempInTheirVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the sandbox")
	}
	dir := t.TempDir()
	out, err := run(installing(context.Background(), dir), []string{"PATH=/usr/bin:/bin", "HOME=/nowhere"}, dir, "/bin/sh", "-c", `echo "$HOME"; echo "$TMPDIR"; touch "$TMPDIR/t" "$HOME/h"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Fields(string(out)) {
		if !isUnder(line, dir) {
			t.Fatalf("%q is outside the install %s", line, dir)
		}
	}
}

// LYK_TEST_REAL_LANG=python (or r, julia) downloads and installs that base,
// and the Python base first, into its real capped volume, then runs it from
// its sealed, read-only mount and logs what it uses.
func TestARealBaseInstallsIntoASealedVolume(t *testing.T) {
	lang := os.Getenv("LYK_TEST_REAL_LANG")
	if lang == "" {
		t.Skip("set LYK_TEST_REAL_LANG=python, r or julia to download and install a base")
	}
	b := realVolumeBases(t)
	t.Cleanup(func() {
		if dir := b.mountDir(Julia); b.vol.mounted(dir) {
			b.vol.detach(context.Background(), dir)
		}
	})
	start := time.Now()
	if err := b.Ensure(context.Background(), lang); err != nil {
		t.Fatal(err)
	}
	t.Logf("installed in %s", time.Since(start).Round(time.Second))
	s := newRealStudy(b, t.TempDir())
	name, args, err := s.Interpreter(lang)
	if err != nil {
		t.Fatal(err)
	}
	code := map[string][]string{
		Python: {"-c", "import numpy, pandas, scipy, matplotlib, statsmodels, ipykernel; print(numpy.__version__)"},
		R:      {"-e", "library(tidyverse); library(IRkernel); cat(R.version.string)"},
		Julia:  {"-e", "using DataFrames, CSV, IJulia; print(VERSION)"},
	}[lang]
	cmd := exec.Command(name, append(args, code...)...)
	cmd.Env = append(os.Environ(), s.Env()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	} else {
		t.Logf("runs: %s", strings.TrimSpace(string(out)))
	}
	for _, l := range []string{Python, lang} {
		dir := b.mountDir(l)
		if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o644); err == nil {
			t.Fatalf("sealed %s base is writable", l)
		}
		du, _ := exec.Command("du", "-sh", dir).Output()
		img, _ := os.Stat(b.sealedImage(l))
		t.Logf("%s base uses %s; sealed image is %d MB", l, strings.Fields(string(du))[0], img.Size()>>20)
	}
}

// newRealStudy prepares a study's folders without a sandbox, which this test
// stands in for.
func newRealStudy(b *Bases, dir string) *Study {
	s := ForStudy(b, dir)
	s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env, cmd.Dir = append(os.Environ(), env...), dir
		return cmd.Output()
	}
	return s
}
