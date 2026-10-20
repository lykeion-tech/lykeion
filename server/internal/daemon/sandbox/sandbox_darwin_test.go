package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `open` hands an app to LaunchServices, which starts it outside the
// sandbox. The probe app copies the secret out when it runs.
func TestLaunchServicesCannotStartAnApp(t *testing.T) {
	p, secret, run := policyFixture(t)
	marker := filepath.Join(tempDir(t), "marker")
	app := filepath.Join(run, "Probe.app")
	exe := filepath.Join(mkdir(t, filepath.Join(app, "Contents", "MacOS")), "Probe")
	script := "#!/bin/sh\ncat " + shq(secret) + " > " + shq(marker) + "\n"
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` +
		`<key>CFBundleExecutable</key><string>Probe</string>` +
		`<key>CFBundleIdentifier</key><string>dev.lykeion.sandbox-probe</string>` +
		`<key>CFBundlePackageType</key><string>APPL</string></dict></plist>`
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(lsregister, "-u", app).Run() })
	// Control: unsandboxed, the app does run and copy the secret.
	if out, err := exec.Command("/usr/bin/open", "-g", "-n", app).CombinedOutput(); err != nil {
		t.Fatalf("control open: %v %s", err, out)
	}
	if !appeared(marker, 10*time.Second) {
		t.Fatal("control: the probe app never ran outside the sandbox")
	}
	os.Remove(marker)

	out, err := sandboxed(t, p, "/usr/bin/open -g -n "+shq(app))
	if err == nil {
		t.Errorf("open succeeded in the sandbox: %q", out)
	}
	if appeared(marker, 3*time.Second) {
		b, _ := os.ReadFile(marker)
		t.Fatalf("the app ran outside the sandbox and read the secret (%d bytes)", len(b))
	}
}

const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

func appeared(path string, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return true
		}
	}
	return false
}

// The clipboard is not readable. Only the exit status is looked at, so the
// person's clipboard never reaches the test log.
func TestPasteboardIsUnreachable(t *testing.T) {
	p, _, _ := policyFixture(t)
	if _, err := sandboxed(t, p, "/usr/bin/pbpaste > /dev/null 2>&1"); err == nil {
		t.Fatal("pbpaste succeeded in the sandbox")
	}
}

// Shared and per-user locations outside the policy stay closed.
func TestPrivateLocationsAreRefused(t *testing.T) {
	p, _, _ := policyFixture(t)
	cache, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_CACHE_DIR").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/Volumes", "/Users/Shared", strings.TrimSpace(string(cache)), "/private/tmp", "/tmp/", "/Users"} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		refused(t, p, "ls "+shq(dir))
	}
}

// The system's own tools and interpreter still start.
func TestSystemToolsRun(t *testing.T) {
	p, _, _ := policyFixture(t)
	for script, want := range map[string]string{
		`/usr/bin/python3 -c "import json, ssl, socket; print('py ok')"`: "py ok\n",
		`curl -q --version > /dev/null && echo curl ok`:                  "curl ok\n",
	} {
		if out, err := sandboxed(t, p, script); err != nil || !strings.HasSuffix(out, want) {
			t.Errorf("%s: err=%v out=%q", script, err, out)
		}
	}
}

// The person's own data under the package managers' var folders stays
// closed while the rest of their trees is readable.
func TestPackageManagerDataIsRefused(t *testing.T) {
	p, _, _ := policyFixture(t)
	prof, err := p.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	deny := `(deny file-read* (subpath "/opt/homebrew/var") (subpath "/usr/local/var"))`
	if i := strings.Index(prof, deny); i < 0 || i < strings.LastIndex(prof, "(allow file-read*") {
		t.Fatalf("var folders not denied after the read allows:\n%s", prof)
	}
	if _, err := os.Stat("/opt/homebrew/var"); err != nil {
		t.Skip("no /opt/homebrew/var")
	}
	refused(t, p, "ls /opt/homebrew/var")
	if out, err := sandboxed(t, p, "ls /opt/homebrew/bin > /dev/null && echo ok"); err != nil || out != "ok\n" {
		t.Fatalf("/opt/homebrew/bin: err=%v out=%q", err, out)
	}
}

// A link below the home folder that leads out of it never reaches a profile,
// so the folder it points at stays untouched.
func TestLinkOutOfHomeGrantsNothing(t *testing.T) {
	p, _, _ := policyFixture(t)
	outside := tempDir(t)
	link := filepath.Join(p.Home, "tmp")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	p.Write = append(p.Write, link)
	if cmd, err := p.Command(context.Background(), "/bin/sh", "-c", "echo x > "+shq(filepath.Join(link, "planted"))); err == nil {
		_ = cmd.Run()
		t.Error("a link out of the home folder was accepted as a write folder")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("wrote through the link into a folder outside the policy")
	}
}

// A socket the CLI keeps in its own folder of the run is out of reach,
// though the folder around it is a write folder. (bwrap's read-only mount
// does not stop a connect; see the package doc.)
func TestSocketsInReadOnlyPathsAreUnreachable(t *testing.T) {
	p, _, _ := fixtureIn(t, shortDir(t))
	run := mkdir(t, filepath.Join(p.Home, "r"))
	cli := mkdir(t, filepath.Join(run, ".cli-home"))
	p.Write, p.ReadOnly = []string{run}, []string{cli}
	sock := filepath.Join(cli, "c.sock")
	serveUnix(t, sock)
	out, err := sandboxed(t, p, "curl -q -sS --unix-socket "+shq(sock)+" http://x/")
	if err == nil || strings.Contains(out, "hello") {
		t.Fatalf("socket %s reachable from the sandbox: err=%v out=%q", sock, err, out)
	}
}
