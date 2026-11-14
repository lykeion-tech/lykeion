package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestProjectionExposesOnlyItsStateAndCache(t *testing.T) {
	if err := Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	state, cache := filepath.Join(home, "daemon"), filepath.Join(home, "cache")
	ownHome, ownCache := filepath.Join(state, "guests", "B", "codex"), filepath.Join(cache, "guests", "B")
	otherHome, otherCache := filepath.Join(state, "guests", "C", "codex"), filepath.Join(cache, "guests", "C")
	for _, dir := range []string{ownHome, ownCache, otherHome, otherCache, filepath.Join(home, "Documents")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "planted"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Home: home, HomeRead: false, RestrictedRoots: []string{state, cache}, Write: []string{ownHome, ownCache}}
	for _, dir := range []string{otherHome, otherCache, filepath.Join(home, "Documents")} {
		path := filepath.Join(dir, "planted")
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "fixture" {
			t.Fatal("missing denial fixture")
		}
		cmd, err := p.Command(t.Context(), "/bin/cat", path)
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("read another user's fixture: %s", output)
		}
		t.Logf("denied %s: %s", filepath.Base(dir), output)
	}
	cmd, err := p.Command(t.Context(), "/bin/sh", "-c", `cat "$1/planted" >/dev/null && cat "$2/planted" >/dev/null && echo own > "$1/writable" && echo own > "$2/writable"`, "sh", ownHome, ownCache)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("own grants failed: %v %s", err, output)
	}
}

func TestGuestPolicyHasNoKeychainServices(t *testing.T) {
	p := Policy{Home: t.TempDir()}
	profile, err := p.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile, `(global-name "com.apple.SecurityServer")`) || strings.Contains(profile, `(global-name "com.apple.securityd.xpc")`) {
		t.Fatal("SecurityServer permission scope changed")
	}
	if strings.Contains(profile, "Keychains") {
		t.Fatal("SecurityServer permission added file access")
	}
}
