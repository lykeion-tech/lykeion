package daemon

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestAGuestCannotReadTheOwnersHome(t *testing.T) { testGuestSandbox(t, false) }
func TestAGuestCanUseItsOwnHome(t *testing.T)        { testGuestSandbox(t, true) }

func TestGuestProcessEnvironmentProbe(t *testing.T) {
	raw := os.Getenv("LYK_GUEST_PROCESS_PROBE")
	if raw == "" {
		t.Skip("subprocess-only probe")
	}
	pid, err := strconv.Atoi(raw)
	must(t, err)
	_, unrelatedErr := unix.SysctlRaw("kern.hostname")
	t.Logf("unrelated kern.hostname err=%v", unrelatedErr)
	for name, read := range map[string]func(int) ([]byte, error){"named": func(pid int) ([]byte, error) { return unix.SysctlRaw("kern.procargs2", pid) }, "numeric": rawProcessEnvironment} {
		data, err := read(pid)
		if err != nil {
			t.Logf("%s process environment denied: %v", name, err)
			continue
		}
		if bytes.Contains(data, []byte("LYK_PROCESS_PROBE=isolated-fixture")) {
			t.Errorf("%s EXPOSED_TEST_PROCESS_ENV", name)
		}
	}
}

func TestGuestExternalEnvironmentFixture(t *testing.T) {
	if os.Getenv("LYK_GUEST_EXTERNAL_FIXTURE") != "1" {
		t.Skip("subprocess-only fixture")
	}
	fmt.Println("READY")
	time.Sleep(30 * time.Second)
}

func TestUnsupportedMacGuestEnvironmentEscapeDiagnostic(t *testing.T) {
	if os.Getenv("LYK_UNSUPPORTED_MAC_GUEST_DIAGNOSTIC") != "1" {
		t.Skip("opt-in expected-failing reproduction of unsupported macOS guest boundary")
	}
	self, err := os.Executable()
	must(t, err)
	external := exec.Command(self, "-test.run=^TestGuestExternalEnvironmentFixture$")
	external.Env = []string{"LYK_PROCESS_PROBE=isolated-fixture", "LYK_GUEST_EXTERNAL_FIXTURE=1", "PATH=/usr/bin:/bin"}
	ready, err := external.StdoutPipe()
	must(t, err)
	must(t, external.Start())
	defer func() { external.Process.Kill(); external.Wait() }()
	line, err := bufio.NewReader(ready).ReadString('\n')
	must(t, err)
	if line != "READY\n" {
		t.Fatal("external fixture did not become ready")
	}
	pid := external.Process.Pid
	name, nameErr := unix.SysctlRaw("sysctl.name", 1, 49, pid)
	t.Logf("probe query name=%q err=%v", name, nameErr)
	control, err := unix.SysctlRaw("kern.procargs2", pid)
	must(t, err)
	if !bytes.Contains(control, []byte("LYK_PROCESS_PROBE=isolated-fixture")) {
		t.Fatal("external environment fixture is not observable without sandbox")
	}
	d, run, guest, _, paths := guestPolicyFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := d.cliSandbox(run, nil, false, guest)(ctx, paths, "/bin/ps", "eww", "-p", strconv.Itoa(pid))
	must(t, err)
	output, _ := cmd.CombinedOutput()
	if bytes.Contains(output, []byte("LYK_PROCESS_PROBE=isolated-fixture")) {
		t.Fatal("ps read another process environment")
	}
	cmd, err = d.cliSandbox(run, nil, false, guest)(ctx, paths, self, "-test.run=^TestGuestProcessEnvironmentProbe$", "-test.v")
	must(t, err)
	if os.Getenv("LYK_TEST_GUEST_SYSCTL_DENY") == "1" {
		if !strings.Contains(cmd.Args[2], "(allow sysctl-read)") {
			t.Fatal("missing sysctl rule to replace")
		}
		cmd.Args[2] = strings.Replace(cmd.Args[2], "(allow sysctl-read)", `(allow sysctl-read (sysctl-name-prefix "hw.") (sysctl-name "kern.osrelease") (sysctl-name "kern.osversion") (sysctl-name "kern.ostype") (sysctl-name "kern.version"))`, 1)
	}
	cmd.Env = []string{"LYK_GUEST_PROCESS_PROBE=" + strconv.Itoa(pid), "HOME=" + os.Getenv("HOME"), "PATH=/usr/bin:/bin"}
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("direct process probe: %v %s", err, output)
	}
	t.Logf("direct sandboxed process probe: %s", output)
}

func rawProcessEnvironment(pid int) ([]byte, error) {
	mib := []int32{1, 49, int32(pid)}
	var size uintptr
	_, _, errno := unix.Syscall6(unix.SYS_SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), 0, uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	if size == 0 {
		return nil, nil
	}
	data := make([]byte, size)
	_, _, errno = unix.Syscall6(unix.SYS_SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	return data[:size], nil
}
