//go:build darwin || linux

package desktopupdate

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestUnverifiedPlatformGatesRemainClosed(t *testing.T) {
	for _, target := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}, {"linux", "386"}} {
		if platformProofReady(target[0], target[1]) {
			t.Fatalf("unverified target enabled: %s/%s", target[0], target[1])
		}
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
	stop, err := armStreamPipes(ctx, input, output)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() { _, err := (&deadlineInput{ctx: ctx, file: input}).Read(make([]byte, 1)); readDone <- err }()
	go func() { _, err := output.Write(make([]byte, 1024*1024)); writeDone <- err }()
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
