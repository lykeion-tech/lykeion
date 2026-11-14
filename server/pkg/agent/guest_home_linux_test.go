package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuestHomeCopiesOnlyLinuxClaudeSignIn(t *testing.T) {
	owner, guest := t.TempDir(), filepath.Join(t.TempDir(), "guest")
	for _, name := range []string{".credentials.json", "settings.json", "skills/private", "projects/session"} {
		path := filepath.Join(owner, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sign-in-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PrepareGuestHome("claude", owner, guest); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(guest)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != ".credentials.json" {
		t.Fatal("copied personal Claude state")
	}
	data, err := os.ReadFile(filepath.Join(guest, ".credentials.json"))
	if err != nil || string(data) != "sign-in-fixture" {
		t.Fatal("missing copied sign-in")
	}
}
