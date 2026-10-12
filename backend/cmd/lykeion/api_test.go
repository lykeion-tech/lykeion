package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialDestinationAndPermissions(t *testing.T) {
	for _, s := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?secret=value"} {
		if _, err := validEndpoint(s); err == nil {
			t.Fatalf("unsafe origin %s", s)
		}
	}
	for _, s := range []string{"http://127.0.0.1:18431", "https://example.com"} {
		if _, err := validEndpoint(s); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := privateRead(path); err == nil {
		t.Fatal("public credential file accepted")
	}
	os.Chmod(path, 0600)
	if _, err := privateRead(path); err != nil {
		t.Fatal(err)
	}
}
