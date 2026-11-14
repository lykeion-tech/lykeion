//go:build darwin || linux

package localfiles

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestSerializesWritersAndKeepsOtherPins(t *testing.T) {
	folder, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer folder.Close()
	first, err := folder.LockManifest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if second, err := folder.LockManifest(ctx); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("lock did not serialize: %v", err)
	}
	first.Entries["a.py"] = Pin{1, "a"}
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second, err := folder.LockManifest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second.Entries["b.py"] = Pin{2, "b"}
	if err := second.Save(); err != nil {
		t.Fatal(err)
	}
	second.Close()
	third, err := folder.LockManifest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if third.Entries["a.py"].N != 1 || third.Entries["b.py"].N != 2 {
		t.Fatalf("pins %+v", third.Entries)
	}
}
func TestTargetDoesNotFollowReplacedParentOrLeaf(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(dir, ".lyk")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	folder, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer folder.Close()
	target, err := folder.Prepare("data/secret")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := os.Rename(filepath.Join(dir, "data"), filepath.Join(dir, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(protected, filepath.Join(dir, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(protected, "secret"), filepath.Join(dir, "old/secret")); err != nil {
		t.Fatal(err)
	}
	if err := target.Replace(func(w io.Writer) error { _, err := io.WriteString(w, "new"); return err }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(protected, "secret"))
	if string(b) != "secret" {
		t.Fatalf("protected target changed: %q", b)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "old/secret"))
	if string(b) != "new" {
		t.Fatalf("atomic replacement missing: %q", b)
	}
}
func TestManifestRefusesInvalidPinsAndTrailingJSON(t *testing.T) {
	for _, body := range []string{`{"a.py":{"n":-1,"sha256":"hash"}}`, `{"a.py":{"n":0,"sha256":"hash"}}`, `{"a.py":{"n":1,"sha256":""}}`, `{} {"a.py":{"n":1,"sha256":"hash"}}`, `null`} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, ".lyk"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".lyk/fill.json"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			folder, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer folder.Close()
			m, err := folder.LockManifest(t.Context())
			if err == nil {
				m.Close()
				t.Fatalf("accepted invalid manifest %s", body)
			}
		})
	}
}

func TestTargetCreateNeverClobbersConcurrentCreation(t *testing.T) {
	dir := t.TempDir()
	folder, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer folder.Close()
	target, err := folder.Prepare("new.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	err = target.Create(func(w io.Writer) error {
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("local"), 0600); err != nil {
			return err
		}
		_, err := io.WriteString(w, "remote")
		return err
	})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected no-clobber conflict, got %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil || string(b) != "local" {
		t.Fatalf("local creation lost: %q %v", b, err)
	}
	other, err := folder.Prepare("safe.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Create(func(w io.Writer) error { _, err := io.WriteString(w, "remote"); return err }); err != nil {
		t.Fatal(err)
	}
}
