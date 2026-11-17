//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

func TestInheritedDescriptorsDoNotReachChildren(t *testing.T) {
	// Opened without close-on-exec, like a descriptor inherited from a parent.
	fd, err := syscall.Open(os.DevNull, syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	probe := "[ -e /dev/fd/" + strconv.Itoa(fd) + " ]"
	if exec.Command("/bin/sh", "-c", probe).Run() != nil {
		t.Skip("this system does not show inherited descriptors under /dev/fd")
	}
	closeInheritedOnExec()
	if exec.Command("/bin/sh", "-c", probe).Run() == nil {
		t.Fatal("a child still inherits the descriptor")
	}
}
