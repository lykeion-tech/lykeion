package sandbox

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestNoNetworkSeccompArchitectures(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		b, err := noNetworkFilter(arch)
		if err != nil || len(b) == 0 || len(b)%8 != 0 {
			t.Fatal(arch, len(b), err)
		}
		if binary.LittleEndian.Uint32(b[4:8]) != 4 {
			t.Fatal("missing architecture load")
		}
	}
	if _, err := noNetworkFilter("386"); err == nil {
		t.Fatal("unsupported ABI allowed")
	}
}

func TestNoNetworkCommandClosesOnlyOwnedDescriptors(t *testing.T) {
	for i := 0; i < 50; i++ {
		caller, e := os.CreateTemp(t.TempDir(), "caller")
		if e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command("/missing-command")
		cmd.ExtraFiles = []*os.File{caller}
		if e = attachNoNetworkFilter(cmd); e != nil {
			caller.Close()
			t.Fatal(e)
		}
		owned := cmd.ExtraFiles[1]
		if cmd.Args[1] != "--seccomp" || cmd.Args[2] != "4" {
			t.Fatal(cmd.Args)
		}
		if e = cmd.Run(); e == nil {
			t.Fatal("unexpected Start success")
		}
		CloseCommand(cmd)
		CloseCommand(cmd)
		if _, e = owned.Stat(); !errors.Is(e, os.ErrClosed) {
			t.Fatal("sandbox descriptor left open", e)
		}
		if _, e = caller.Stat(); e != nil {
			t.Fatal("caller descriptor closed", e)
		}
		caller.Close()
	}
}

func TestNoNetworkFilterDeniesAllSupportedSocketABIs(t *testing.T) {
	evaluate := func(b []byte, arch, nr uint32) uint32 {
		var a uint32
		for pc := 0; pc < len(b)/8; pc++ {
			i := b[pc*8:]
			op := binary.LittleEndian.Uint16(i)
			k := binary.LittleEndian.Uint32(i[4:8])
			switch op {
			case 0x20:
				if k == 4 {
					a = arch
				} else {
					a = nr
				}
			case 0x15:
				if a == k {
					pc += int(i[2])
				} else {
					pc += int(i[3])
				}
			case 0x35:
				if a >= k {
					pc += int(i[2])
				} else {
					pc += int(i[3])
				}
			case 0x06:
				return k
			default:
				t.Fatalf("unknown opcode %x", op)
			}
		}
		t.Fatal("no filter result")
		return 0
	}
	for _, test := range []struct {
		arch   string
		audit  uint32
		denied []uint32
	}{{"amd64", 0xc000003e, []uint32{41, 42, 43, 44, 46, 49, 50, 288, 307, 425, 426, 427}}, {"arm64", 0xc00000b7, []uint32{198, 200, 201, 202, 203, 206, 211, 242, 269, 425, 426, 427}}} {
		b, e := noNetworkFilter(test.arch)
		if e != nil {
			t.Fatal(e)
		}
		for _, nr := range test.denied {
			if got := evaluate(b, test.audit, nr); got != 0x50001 {
				t.Fatal(test.arch, nr, got)
			}
		}
		if got := evaluate(b, test.audit, 1); got != 0x7fff0000 {
			t.Fatal("ordinary syscall denied", got)
		}
		if got := evaluate(b, 0x40000003, 102); got != 0x80000000 {
			t.Fatal("compat socketcall allowed", got)
		}
		if test.arch == "amd64" && evaluate(b, test.audit, 0x40000029) != 0x80000000 {
			t.Fatal("x32 socket allowed")
		}
	}
}

func TestNoNetworkDescriptorLifecycleAfterCompletion(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "exit 0")
			if cancelled {
				cmd = exec.CommandContext(ctx, "/bin/sh", "-c", "exec sleep 10")
			}
			original := append([]string(nil), cmd.Args...)
			if e := attachNoNetworkFilter(cmd); e != nil {
				t.Fatal(e)
			}
			owned := cmd.ExtraFiles[0]
			// Exercise descriptor ownership independently of an available Linux sandbox.
			cmd.Args = original
			if e := cmd.Start(); e != nil {
				CloseCommand(cmd)
				t.Fatal(e)
			}
			if cancelled {
				cancel()
			}
			e := cmd.Wait()
			CloseCommand(cmd)
			if (e != nil) != cancelled {
				t.Fatal(e)
			}
			if _, e = owned.Stat(); !errors.Is(e, os.ErrClosed) {
				t.Fatal("descriptor retained after completion", e)
			}
		})
	}
}
