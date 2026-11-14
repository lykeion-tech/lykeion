//go:build darwin

package desktopupdate

import (
	"encoding/binary"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateACLAllowsNoACLAndDeniesGrants(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", dir).Run() })
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if !privateACL(fd) {
		t.Fatal("private directory without ACL was rejected")
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone deny delete", dir).CombinedOutput(); err != nil {
		t.Fatalf("set deny ACL: %v %s", err, output)
	}
	if !privateACL(fd) {
		t.Fatal("deny-only ACL was rejected")
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow readattr", dir).CombinedOutput(); err != nil {
		t.Fatalf("set allow ACL: %v %s", err, output)
	}
	if privateACL(fd) {
		t.Fatal("allow ACL was accepted")
	}
}

func TestPrivateACLRejectsMalformedBuffers(t *testing.T) {
	var noACL [12]byte
	binary.LittleEndian.PutUint32(noACL[0:4], 12)
	binary.LittleEndian.PutUint32(noACL[4:8], 8)
	if !privateACLBytes(noACL[:]) {
		t.Fatal("valid empty ACL rejected")
	}
	for _, tc := range []struct{ total, offset, size uint32 }{
		{11, 8, 0}, {12, 100, 0}, {100, 8, 0}, {12, 8, 44}, {12, 7, 0},
	} {
		b := noACL
		binary.LittleEndian.PutUint32(b[0:4], tc.total)
		binary.LittleEndian.PutUint32(b[4:8], tc.offset)
		binary.LittleEndian.PutUint32(b[8:12], tc.size)
		if privateACLBytes(b[:]) {
			t.Fatalf("accepted malformed ACL: %+v", tc)
		}
	}
	var deny [80]byte
	binary.LittleEndian.PutUint32(deny[0:4], 80)
	binary.LittleEndian.PutUint32(deny[4:8], 8)
	binary.LittleEndian.PutUint32(deny[8:12], 68)
	binary.LittleEndian.PutUint32(deny[12:16], 0x012cc16d)
	binary.LittleEndian.PutUint32(deny[48:52], 1)
	binary.LittleEndian.PutUint32(deny[72:76], 2)
	if !privateACLBytes(deny[:]) {
		t.Fatal("valid deny ACL rejected")
	}
	deferred := deny
	binary.LittleEndian.PutUint32(deferred[52:56], 1<<16)
	if privateACLBytes(deferred[:]) {
		t.Fatal("deferred inheritance accepted")
	}
	unknownACE := deny
	binary.LittleEndian.PutUint32(unknownACE[72:76], 2|1<<31)
	if privateACLBytes(unknownACE[:]) {
		t.Fatal("unknown ACE flag accepted")
	}
}
