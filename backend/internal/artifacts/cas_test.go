package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContentAddressedBytesRejectTampering(t *testing.T) {
	root := t.TempDir()
	data := []byte("sealed scientific configuration")
	hash, err := Put(root, data)
	if err != nil {
		t.Fatal(err)
	}
	same, err := Put(root, data)
	if err != nil || same != hash {
		t.Fatal("same bytes changed address", err)
	}
	path := filepath.Join(root, hash)
	os.Chmod(path, 0600)
	os.WriteFile(path, []byte("altered"), 0600)
	if _, err = Read(root, hash); err == nil {
		t.Fatal("changed bytes accepted")
	}
	if _, err = Read(root, "../outside"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
