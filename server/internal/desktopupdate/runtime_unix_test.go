//go:build darwin || linux

package desktopupdate

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestUnverifiedPlatformGatesRemainClosed(t *testing.T) {
	for _, target := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}, {"linux", "386"}} {
		if platformProofReady(target[0], target[1]) {
			t.Fatalf("unverified target enabled: %s/%s", target[0], target[1])
		}
	}
}

func TestStreamPipeOwnershipAndFailureRestoreFlags(t *testing.T) {
	in, inWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer inWriter.Close()
	outReader, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outReader.Close()
	defer out.Close()
	inFD, outFD := int(in.Fd()), int(out.Fd())
	inFlags, err := unix.FcntlInt(uintptr(inFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	outFlags, err := unix.FcntlInt(uintptr(outFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	pipes, err := openStreamPipes(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	pipes.close()
	pipes.close()
	gotIn, err := unix.FcntlInt(uintptr(inFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	gotOut, err := unix.FcntlInt(uintptr(outFD), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if gotIn != inFlags || gotOut != outFlags {
		t.Fatal("stdio flags not restored")
	}
	if _, err := inWriter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if n, err := in.Read(b); err != nil || n != 1 || b[0] != 'x' {
		t.Fatal("original stdin was closed")
	}
	if _, err := openStreamPipes(context.Background(), in, outReader); err == nil {
		t.Fatal("accepted read-only output")
	}
	gotIn, err = unix.FcntlInt(uintptr(inFD), unix.F_GETFL, 0)
	if err != nil || gotIn != inFlags {
		t.Fatal("failure did not restore input flags")
	}
	if _, err := openStreamPipes(context.Background(), in, in); err == nil {
		t.Fatal("accepted same descriptor for input and output")
	}
	regular, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	if _, err := openStreamPipes(context.Background(), regular, out); err == nil {
		t.Fatal("accepted regular input")
	}
}

func TestStreamPipesCancellationInterruptsBackpressure(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	reader, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer output.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipes, err := openStreamPipes(ctx, input, output)
	if err != nil {
		t.Fatal(err)
	}
	defer pipes.close()
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() { _, err := (&deadlineInput{ctx: ctx, file: pipes.input}).Read(make([]byte, 1)); readDone <- err }()
	go func() { _, err := pipes.output.Write(make([]byte, 1024*1024)); writeDone <- err }()
	cancel()
	for _, result := range []<-chan error{readDone, writeDone} {
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("canceled pipe completed without error")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canceled pipe blocked")
		}
	}
}

func TestDisabledStreamDoesNotReadOrWrite(t *testing.T) {
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputRead.Close()
	defer inputWrite.Close()
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outputRead.Close()
	defer outputWrite.Close()
	if err := StreamVerified(context.Background(), inputRead, outputWrite); ErrorCategory(err) != Unsupported {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := outputWrite.Close(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if n, _ := outputRead.Read(buf); n != 0 {
		t.Fatal("disabled stream wrote output")
	}
}

// This child is started by a Node process so these descriptors match Electron child stdio.
func TestNodePipeChild(t *testing.T) {
	mode := os.Getenv("LYK_TEST_NODE_PIPE_CHILD")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipes, err := openStreamPipes(ctx, os.Stdin, os.Stdout)
	if err != nil {
		os.Exit(21)
	}
	switch mode {
	case "normal":
		if _, err := pipes.output.WriteString("READY\n"); err != nil {
			os.Exit(22)
		}
		request, err := io.ReadAll(io.LimitReader(&deadlineInput{ctx: ctx, file: pipes.input}, 8))
		if err != nil || string(request) != "STREAM\n" {
			os.Exit(23)
		}
		if _, err := pipes.output.WriteString("DONE\n"); err != nil {
			os.Exit(24)
		}
	case "read-timeout":
		if err := pipes.input.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			os.Exit(25)
		}
		if _, err := pipes.input.Read(make([]byte, 1)); err == nil {
			os.Exit(26)
		}
	case "write-timeout":
		if err := pipes.output.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			os.Exit(27)
		}
		if _, err := pipes.output.Write(make([]byte, 8<<20)); err == nil {
			os.Exit(28)
		}
	case "cancel":
		time.AfterFunc(100*time.Millisecond, cancel)
		if _, err := (&deadlineInput{ctx: ctx, file: pipes.input}).Read(make([]byte, 1)); err == nil {
			os.Exit(29)
		}
	default:
		os.Exit(30)
	}
	pipes.close()
	os.Exit(0)
}
