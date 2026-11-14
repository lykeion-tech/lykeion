//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHealthReadKeepsOpenedFileWhenPathChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	want := HealthRecord{Port: 1234, PID: 123, Started: "start", Nonce: "nonce", ServerURL: "https://example.test", DaemonID: "daemon"}
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
	if err := os.Symlink(path+".old", path); err != nil {
		t.Fatal(err)
	}
	got, err := decodeHealthRecord(f)
	if err != nil || got != want {
		t.Fatalf("opened record changed: %+v, %v", got, err)
	}
	if _, err := readHealthRecord(path); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHealthRecord(path); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestHealthReadRejectsSubstitutionBeforeOpen(t *testing.T) {
	for _, replacement := range []string{"symlink", "fifo", "regular"} {
		t.Run(replacement, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "health.json")
			record := HealthRecord{Port: 1234, PID: 123, Started: "start", Nonce: "nonce", ServerURL: "https://example.test", DaemonID: "daemon"}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = readHealthRecordWithOpen(path, func(path string) (*os.File, error) {
				if err := os.Rename(path, path+".old"); err != nil {
					return nil, err
				}
				switch replacement {
				case "symlink":
					err = os.Symlink(path+".old", path)
				case "fifo":
					err = unix.Mkfifo(path, 0600)
				case "regular":
					err = os.WriteFile(path, data, 0600)
				}
				if err != nil {
					return nil, err
				}
				return openReportFile(path)
			})
			if err == nil {
				t.Fatalf("accepted %s substituted between check and open", replacement)
			}
		})
	}
}

func TestHealthReadRejectsSubstitutionAfterOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	record := HealthRecord{Port: 1234, PID: 123, Started: "start", Nonce: "nonce", ServerURL: "https://example.test", DaemonID: "daemon"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = readHealthRecordWithOpen(path, func(path string) (*os.File, error) {
		f, err := openReportFile(path)
		if err != nil {
			return nil, err
		}
		if err = os.Rename(path, path+".old"); err != nil {
			f.Close()
			return nil, err
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			f.Close()
			return nil, err
		}
		return f, nil
	})
	if err == nil {
		t.Fatal("accepted replacement after opening original")
	}
}
