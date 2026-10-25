package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func TestAScriptCannotReadAServiceFolder(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	door, home := realSandboxDoor(t, l, dir)
	root := filepath.Join(home, "state", "services")
	code := filepath.Join(root, "workspace", "card", "evaluator", "code")
	must(t, os.MkdirAll(code, 0700))
	must(t, os.WriteFile(filepath.Join(code, "answers.txt"), []byte("PRIVATE ANSWER"), 0600))
	for _, path := range []string{root, filepath.Join(code, "answers.txt")} {
		script := fmt.Sprintf("import subprocess\nsubprocess.run([%q, %q], check=True)\n", "/bin/cat", path)
		if path == root {
			script = fmt.Sprintf("import subprocess\nsubprocess.run(['/bin/ls', %q], check=True)\n", path)
		}
		must(t, os.WriteFile(filepath.Join(dir, "probe.py"), []byte(script), 0600))
		got, err := useTool(t, door, "run_script", map[string]any{"path": "probe.py"})
		must(t, err)
		if !strings.Contains(got, "Operation not permitted") || strings.Contains(got, "PRIVATE ANSWER") {
			t.Fatalf("%s: %s", path, got)
		}
	}
	for _, grant := range append(slices.Clone(door.policy.Read), door.policy.Write...) {
		if insideStorage(grant, root) || insideStorage(root, grant) {
			t.Fatalf("Door grant exposes services: %s", grant)
		}
	}
}

func TestTheCLISandboxHidesServiceFolders(t *testing.T) {
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}}
	root := filepath.Join(d.cfg.StateDir, "services")
	code := filepath.Join(root, "workspace", "card", "evaluator", "code")
	must(t, os.MkdirAll(code, 0700))
	secret := filepath.Join(code, "answers.txt")
	must(t, os.WriteFile(secret, []byte("PRIVATE ANSWER"), 0600))
	p, err := d.cliPolicy(t.TempDir(), nil, false, agent.CLIPaths{})
	must(t, err)
	if !slices.Contains(p.Hidden, root) {
		t.Fatal("service root is not explicitly hidden")
	}
	for _, probe := range [][]string{{"/bin/ls", root}, {"/bin/cat", secret}} {
		cmd, err := p.Command(context.Background(), probe[0], probe[1:]...)
		must(t, err)
		b, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(b), "Operation not permitted") || strings.Contains(string(b), "PRIVATE ANSWER") {
			t.Fatalf("%v: %s %v", probe, b, err)
		}
	}
}
func TestAServiceCannotWriteOutsideItsState(t *testing.T) {
	root := t.TempDir()
	code := filepath.Join(root, "code")
	state := filepath.Join(root, "state")
	tmp := filepath.Join(root, "tmp")
	for _, p := range []string{code, state, tmp} {
		must(t, os.Mkdir(p, 0700))
	}
	p, err := servicePolicy(code, state, tmp, &envs.Bases{Dir: filepath.Join(root, "bases")})
	must(t, err)
	// Exercise the actual filesystem policy independently of blocked networking.
	p.LoopbackOnly = false
	for _, path := range []string{filepath.Join(code, "forbidden"), filepath.Join(root, "outside"), filepath.Join(state, "allowed"), filepath.Join(tmp, "allowed")} {
		cmd, err := p.Command(context.Background(), "/bin/sh", "-c", fmt.Sprintf("echo probe > %q", path))
		must(t, err)
		out, err := cmd.CombinedOutput()
		if strings.HasSuffix(path, "allowed") {
			if err != nil {
				t.Fatalf("allowed write: %s %v", out, err)
			}
		} else if err == nil || !strings.Contains(string(out), "Operation not permitted") {
			t.Fatalf("forbidden write: %s %v", out, err)
		}
	}
}
func TestAServiceCanBindOnlyToLoopback(t *testing.T) {
	root := t.TempDir()
	code := filepath.Join(root, "code")
	state := filepath.Join(root, "state")
	tmp := filepath.Join(root, "tmp")
	for _, p := range []string{code, state, tmp} {
		must(t, os.Mkdir(p, 0700))
	}
	p, err := servicePolicy(code, state, tmp, &envs.Bases{Dir: filepath.Join(root, "bases")})
	must(t, err)
	addresses := []string{"127.0.0.1", "127.0.0.2", "0.0.0.0", "::", "::1"}
	addrs, err := net.InterfaceAddrs()
	must(t, err)
	found := false
	for _, a := range addrs {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.To4() != nil && !ip.IsLoopback() {
			addresses = append(addresses, ip.String())
			found = true
			break
		}
	}
	if !found {
		t.Log("no configured non-loopback IPv4 interface; that address case unavailable")
	}
	if _, err := p.Command(context.Background(), "/usr/bin/true"); errors.Is(err, sandbox.ErrLoopbackOnlyUnsupported) {
		t.Skip("BLOCKED: macOS exact 127.0.0.1 bind enforcement is unavailable; activation remains refused")
	} else if err != nil {
		t.Fatal(err)
	}
	python := realPython(t)
	for _, address := range addresses {
		t.Run(address, func(t *testing.T) {
			family := "socket.AF_INET"
			if strings.Contains(address, ":") {
				family = "socket.AF_INET6"
			}
			script := fmt.Sprintf(`import socket, errno
step="socket"
try:
 s=socket.socket(%s, socket.SOCK_STREAM)
 step="bind"
 s.bind((%q, 0))
 step="listen"
 s.listen(1)
 print("BOUND")
except OSError as e:
 if e.errno in (errno.EPERM, errno.EACCES): print("DENIED", step)
 elif e.errno in (errno.EAFNOSUPPORT, errno.EPROTONOSUPPORT, errno.EADDRNOTAVAIL): print("UNAVAILABLE", e.errno)
 else: raise
`, family, address)
			cmd, err := p.Command(context.Background(), python, "-c", script)
			must(t, err)
			cmd.Env = serviceEnvironment(nil, nil, "0", state, tmp, &envs.Bases{Dir: filepath.Join(root, "bases")})
			cmd.Dir = code
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bind helper: %v %s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if strings.HasPrefix(got, "UNAVAILABLE") {
				t.Skipf("exact address %s unavailable: %s", address, got)
			}
			want := "DENIED"
			if address == "127.0.0.1" {
				want = "BOUND"
			}
			if got != want && !strings.HasPrefix(got, want+" ") {
				t.Fatalf("exact bind %s: want %s got %s", address, want, got)
			}
			t.Logf("real Seatbelt exact bind %s: %s", address, got)
		})
	}
}

func TestMacOSServiceActivationFailsClosed(t *testing.T) {
	d, c, study := serviceFixture(t)
	d.serviceCommand = nil
	_, err := d.startServices(context.Background(), c, study)
	if err == nil || err.Error() != "hidden service evaluator did not start; see the daemon log" {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(d.cfg.StateDir, "services")); !os.IsNotExist(err) {
		t.Fatal("blocked macOS service fetched private code")
	}
	p := sandbox.Policy{Home: t.TempDir(), LoopbackOnly: true}
	if _, err = p.Command(context.Background(), "/bin/true"); !errors.Is(err, sandbox.ErrLoopbackOnlyUnsupported) {
		t.Fatalf("policy bypass: %v", err)
	}
}

func TestDoorExplicitlyHidesPrivateDaemonPaths(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	root := t.TempDir()
	supervisor := &daemon{cfg: Config{StateDir: filepath.Join(root, "daemon")}}
	private := supervisor.privatePaths()
	door := newRunDoor(t, l, dir, func(d *Door) {
		d.PrivatePaths = private
		d.interp = func(lang string) (string, []string, error) {
			if _, _, err := d.Study.Interpreter(lang); err != nil {
				return "", nil, err
			}
			return realPython(t), nil, nil
		}
	}, envs.Python)
	must(t, door.sandboxReady())
	for _, hidden := range private {
		if !slices.Contains(door.policy.Hidden, hidden) {
			t.Fatalf("Door missing private exclusion %s", hidden)
		}
	}
	// Simulate another run creating service files and logging a private failure
	// after this ordinary Door has already built its policy.
	code := filepath.Join(private[0], "workspace", "card", "evaluator", "code")
	must(t, os.MkdirAll(code, 0700))
	secret := filepath.Join(code, "answer")
	must(t, os.WriteFile(secret, []byte("PRIVATE_SENTINEL"), 0600))
	must(t, os.WriteFile(private[1], []byte("PRIVATE_SENTINEL"), 0600))
	script := fmt.Sprintf("import subprocess\nsubprocess.run(['/bin/cat', %q])\nsubprocess.run(['/bin/ls', %q])\nsubprocess.run(['/bin/cat', %q])\n", secret, private[0], private[1])
	must(t, os.WriteFile(filepath.Join(dir, "private.py"), []byte(script), 0600))
	got, err := useTool(t, door, "run_script", map[string]any{"path": "private.py"})
	must(t, err)
	if strings.Contains(got, "PRIVATE_SENTINEL") || strings.Count(got, "Operation not permitted") != 3 {
		t.Fatalf("private Door read: %s", got)
	}
	for _, grant := range append(slices.Clone(door.policy.Read), door.policy.Write...) {
		for _, hidden := range private {
			if insideStorage(grant, hidden) || insideStorage(hidden, grant) {
				t.Fatalf("private exclusion overlaps grant %s", grant)
			}
		}
	}
}
