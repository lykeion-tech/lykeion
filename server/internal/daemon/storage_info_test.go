package daemon

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/storageinfo"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageMeasureOverlapSymlinkMissingAndCancel(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "env")
	storageMust(t, os.Mkdir(nested, 0700))
	storageMust(t, os.WriteFile(filepath.Join(root, "work"), []byte("hello"), 0600))
	storageMust(t, os.WriteFile(filepath.Join(nested, "base"), []byte("abc"), 0600))
	outside := t.TempDir()
	storageMust(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("must not count"), 0600))
	storageMust(t, os.Symlink(outside, filepath.Join(root, "link")))
	got := measureStorage(context.Background(), Config{Home: root, EnvDir: nested, StateDir: filepath.Join(root, "missing")})
	if len(got.Categories) != 3 || got.Categories[0].Bytes != 5 || got.Categories[1].Bytes != 3 {
		t.Fatalf("wrong exclusive counts: %+v", got)
	}
	if !got.Categories[0].Complete || !got.Categories[1].Complete || got.Categories[2].Complete || got.Categories[2].Error == "" {
		t.Fatalf("missing root became empty: %+v", got)
	}
	if len(got.Filesystems) != 1 || got.Filesystems[0].AvailableBytes <= 0 {
		t.Fatalf("filesystem capacities not deduplicated: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := measureStorage(ctx, Config{Home: root, EnvDir: nested, StateDir: outside})
	for _, c := range cancelled.Categories {
		if c.Complete {
			t.Fatalf("cancelled scan reported complete: %+v", c)
		}
	}
}
func TestStorageMeasureEmptyAndSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	storageMust(t, os.Symlink(root, link))
	got := measureStorage(context.Background(), Config{Home: root, EnvDir: link})
	if !got.Categories[0].Complete || got.Categories[0].Bytes != 0 {
		t.Fatalf("empty root: %+v", got)
	}
	if got.Categories[1].Complete || got.Categories[1].Error == "" {
		t.Fatalf("symlink root followed: %+v", got)
	}
}

func TestStorageReportIsIndependentAndOldServerIsNonfatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := make(chan storageinfo.Report, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/daemon/storage" || r.Header.Get("Authorization") != "Bearer lyk_test" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		var in storageinfo.Report
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		reports <- in
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	ref := runtimeRef{WorkspaceID: uuid.New(), RuntimeID: uuid.New()}
	d := &daemon{cfg: Config{Home: t.TempDir(), EnvDir: t.TempDir(), StateDir: t.TempDir()}, id: "test-daemon", refs: []runtimeRef{ref}, api: &apiclient.Client{Server: srv.URL, Token: "lyk_test"}, failures: make(chan error, 1)}
	done := make(chan struct{})
	go func() { d.storageLoop(ctx); close(done) }()
	select {
	case got := <-reports:
		if got.WorkspaceID != ref.WorkspaceID || got.RuntimeID != ref.RuntimeID || got.DaemonID != d.id || got.Snapshot.Validate() != nil {
			t.Fatalf("incorrect report %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup storage not reported")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("storage worker ignored cancellation")
	}
	select {
	case err := <-d.failures:
		t.Fatalf("telemetry became fatal: %v", err)
	default:
	}
}

func storageMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestStorageUnreadableRootRemainsUnknown(t *testing.T) {
	root := t.TempDir()
	storageMust(t, os.Chmod(root, 0))
	defer os.Chmod(root, 0700)
	if _, err := os.ReadDir(root); err == nil {
		t.Skip("test account bypasses directory permissions")
	}
	got := measureStorage(context.Background(), Config{Home: root})
	if got.Categories[0].Complete || got.Categories[0].Error == "" {
		t.Fatalf("permission error became known empty: %+v", got)
	}
}

func TestStorageDistinctFilesystems(t *testing.T) {
	// Linux's shared-memory filesystem lets this fixture own both roots without
	// traversing any existing machine files or requiring a mount operation.
	second, err := os.MkdirTemp("/dev/shm", "lykeion-storage-test-")
	if err != nil {
		t.Skip("second writable filesystem unavailable")
	}
	defer os.RemoveAll(second)
	first := t.TempDir()
	got := measureStorage(context.Background(), Config{Home: first, EnvDir: second})
	if got.Categories[0].FilesystemID == got.Categories[1].FilesystemID {
		t.Skip("fixture roots share a filesystem")
	}
	if !got.Categories[0].Complete || !got.Categories[1].Complete || len(got.Filesystems) != 2 {
		t.Fatalf("distinct filesystems merged: %+v", got)
	}
}

func TestStorageParentAliasOverlapCountsOnce(t *testing.T) {
	base := t.TempDir()
	actual := filepath.Join(base, "actual")
	nested := filepath.Join(actual, "env")
	alias := filepath.Join(base, "alias")
	storageMust(t, os.MkdirAll(nested, 0700))
	storageMust(t, os.Symlink(actual, alias))
	storageMust(t, os.WriteFile(filepath.Join(actual, "work"), []byte("hello"), 0600))
	storageMust(t, os.WriteFile(filepath.Join(nested, "base"), []byte("abc"), 0600))
	display := filepath.Join(alias, "env")
	got := measureStorage(context.Background(), Config{Home: actual, EnvDir: display})
	if got.Categories[0].Bytes != 5 || got.Categories[1].Bytes != 3 {
		t.Fatalf("parent alias counted overlapping files twice: %+v", got.Categories)
	}
	if got.Categories[1].Path != display {
		t.Fatalf("reported location changed: %q", got.Categories[1].Path)
	}
}
