//go:build darwin || linux

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func noNetworkPolicy(t *testing.T) Policy {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Check(ctx); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	p, _, _ := policyFixture(t)
	p.NoNetwork = true
	return p
}

func networkCommand(t *testing.T, p Policy, name string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, err := p.Command(ctx, name, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseCommand(cmd)
	cmd.Dir = p.Write[0]
	return cmd.CombinedOutput()
}

func TestNoNetworkBlocksOutbound(t *testing.T) {
	p := noNetworkPolicy(t)
	if out, err := networkCommand(t, p, "/usr/bin/curl", "--version"); err != nil {
		t.Fatalf("curl launch control: %v %s", err, out)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("reachable")) }))
	defer srv.Close()
	open := p
	open.NoNetwork = false
	if out, err := networkCommand(t, open, "/usr/bin/curl", "-sS", "--max-time", "5", srv.URL); err != nil || string(out) != "reachable" {
		t.Fatalf("network control: %v %s", err, out)
	}
	for _, target := range []string{srv.URL, "https://example.com"} {
		out, err := networkCommand(t, p, "/usr/bin/curl", "-sS", "--max-time", "5", target)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || strings.Contains(string(out), "sandbox-exec:") || strings.Contains(string(out), "bwrap:") {
			t.Errorf("outbound %s was not denied by the running command: %v %s", target, err, out)
		}
	}
	if os.Getenv("LYK_TEST_NETWORK") == "1" {
		if out, err := networkCommand(t, open, "/usr/bin/curl", "-sS", "--max-time", "5", "https://example.com"); err != nil {
			t.Fatalf("external network control: %v %s", err, out)
		}
	}
}

func TestNoNetworkResolvesNothing(t *testing.T) {
	p := noNetworkPolicy(t)
	py := "/usr/bin/python3"
	if runtime.GOOS == "darwin" {
		// The system python3 can be an xcrun launcher; use its actual interpreter.
		out, err := exec.Command("/usr/bin/xcrun", "--find", "python3").Output()
		if err != nil {
			t.Fatal(err)
		}
		py = strings.TrimSpace(string(out))
	}
	if out, err := networkCommand(t, p, py, "-c", "import socket; print('python-ready')"); err != nil || string(out) != "python-ready\n" {
		t.Fatalf("Python launch control: %v %s", err, out)
	}
	lookup := "import socket; socket.getaddrinfo('example.com', 443); print('resolved')"
	if os.Getenv("LYK_TEST_NETWORK") == "1" {
		open := p
		open.NoNetwork = false
		if out, err := networkCommand(t, open, py, "-c", lookup); err != nil || string(out) != "resolved\n" {
			t.Fatalf("DNS positive control: %v %s", err, out)
		}
	}
	// The positive control above also warms system DNS caches before this probe.
	out, err := networkCommand(t, p, py, "-c", lookup)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.Contains(string(out), "socket.gaierror:") {
		t.Fatalf("DNS was not denied by getaddrinfo: %v %s", err, out)
	}
}

func TestNoNetworkExclusive(t *testing.T) {
	p := Policy{Home: tempDir(t), NoNetwork: true, LoopbackOnly: true}
	for name, build := range map[string]func() error{
		"Seatbelt":   func() error { _, err := p.seatbeltProfile(); return err },
		"bubblewrap": func() error { _, err := p.bwrapArgs("true"); return err },
		"Command":    func() error { _, err := p.Command(context.Background(), "true"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := build(); err == nil || err.Error() != "sandbox: NoNetwork and LoopbackOnly are exclusive" {
				t.Fatalf("exclusive policies: %v", err)
			}
		})
	}
}

func TestNoNetworkBackendPolicies(t *testing.T) {
	p := Policy{Home: tempDir(t), NoNetwork: true}
	prof, err := p.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prof, "(deny network*)") || strings.Contains(prof, "(allow network-") {
		t.Fatalf("Seatbelt permits network: %s", prof)
	}
	args, err := p.bwrapArgs("true")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "--unshare-net") || slices.Contains(args, "--share-net") {
		t.Fatalf("bubblewrap network namespace: %v", args)
	}
}

func TestNoNetworkBlocksLateFilesystemSocket(t *testing.T) {
	t.Logf("real pathname-socket confinement on %s", runtime.GOOS)
	p := noNetworkPolicy(t)
	p.StrictRead = true
	granted, err := os.MkdirTemp("/tmp", "lyk-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(granted)
	granted, err = filepath.EvalSymlinks(granted)
	if err != nil {
		t.Fatal(err)
	}
	p.Read = append(p.Read, granted)
	sock := filepath.Join(granted, "late.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd, err := p.Command(ctx, "/bin/sh", "-c", `touch "$1/ready"; while [ ! -e "$1/go" ]; do sleep .02; done; exec /usr/bin/curl --max-time 2 --unix-socket "$2" http://localhost/`, "sh", p.Write[0], sock)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseCommand(cmd)
	cmd.Dir = p.Write[0]
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()
	for i := 0; i < 300; i++ {
		if _, e := os.Stat(filepath.Join(p.Write[0], "ready")); e == nil {
			break
		}
		if i == 299 {
			t.Fatal("sandbox did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "host socket reached") })}
	go server.Serve(listener)
	defer server.Close()
	open := p
	open.NoNetwork = false
	if runtime.GOOS == "darwin" {
		open.Read = slices.DeleteFunc(slices.Clone(open.Read), func(path string) bool { return path == granted })
		open.Write = append(slices.Clone(open.Write), granted)
	}
	if out, e := networkCommand(t, open, "/usr/bin/curl", "-sS", "--max-time", "2", "--unix-socket", sock, "http://localhost/"); e != nil || string(out) != "host socket reached" {
		t.Fatalf("positive control: %v %s", e, out)
	}
	if err = os.WriteFile(filepath.Join(p.Write[0], "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil || strings.Contains(output.String(), "host socket reached") {
		t.Fatalf("late host socket escaped: %v %s", err, output.String())
	}
}
