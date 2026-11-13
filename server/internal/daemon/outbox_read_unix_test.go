//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReportReadKeepsOpenedFileWhenPathChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	want := reportFixture()
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openReportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("report.json.old", path); err != nil {
		t.Fatal(err)
	}
	got, err := decodeReportFile(f)
	if err != nil || !sameReport(got, want) {
		t.Fatalf("opened file changed: %#v, %v", got, err)
	}
	if _, err := readReport(path); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReport(path); err == nil {
		t.Fatal("FIFO accepted")
	}
}
