package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

func roundTrip(t *testing.T, s Storage) {
	t.Helper()
	ctx := context.Background()
	if err := s.Put(ctx, "ws/card/abc", strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, "ws/card/abc")
	if err != nil {
		t.Fatal(err)
	}
	b, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close blob: read=%v close=%v", readErr, closeErr)
	}
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
	if _, err := s.Get(ctx, "ws/card/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestLocal(t *testing.T) {
	dir := t.TempDir()
	roundTrip(t, Local{Dir: dir})
	for _, key := range []string{"../escape", "/absolute", "ws/../../escape", "", "ws//card"} {
		if err := (Local{Dir: dir}).Put(context.Background(), key, strings.NewReader("x"), 1); err == nil {
			t.Errorf("Put accepted unsafe key %q", key)
		}
	}
}

func TestLocalRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := (Local{Dir: dir}).Put(context.Background(), "linked/blob", strings.NewReader("escape"), 6); err == nil {
		t.Fatal("Put followed a symlink outside storage directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "blob")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside blob exists or could not be checked: %v", err)
	}
}

func TestLocalWriteFailurePreservesExistingBlob(t *testing.T) {
	dir := t.TempDir()
	store := Local{Dir: dir}
	if err := store.Put(context.Background(), "blob", strings.NewReader("before"), 6); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "blob", failingReader{}, 7); err == nil {
		t.Fatal("Put succeeded after reader failure")
	}
	r, err := store.Get(context.Background(), "blob")
	if err != nil {
		t.Fatal(err)
	}
	b, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || string(b) != "before" {
		t.Fatalf("existing blob changed after failed write: %q, read=%v close=%v", b, readErr, closeErr)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("reader failed") }

func TestS3OnMinIO(t *testing.T) {
	ctx := context.Background()
	c, err := tcminio.Run(ctx, "pgsty/minio@sha256:b6bfe7239bfc83fb90d31612d9704d86039dd714f7904b3f1ad68f211e602372")
	if err != nil {
		t.Fatalf("start MinIO container: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Terminate(ctx); err != nil {
			t.Errorf("terminate MinIO container: %v", err)
		}
	})
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", c.Username)
	t.Setenv("AWS_SECRET_ACCESS_KEY", c.Password)
	s, err := NewS3(ctx, "lykeion-test", "http://"+endpoint, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket(ctx); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, s)
}
