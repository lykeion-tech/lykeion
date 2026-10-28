package coresearch

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCleanPath(t *testing.T) {
	for _, p := range []string{"a/b.csv", "a//./b.csv"} {
		got, e := CleanPath(p)
		if e != nil || got != "a/b.csv" {
			t.Errorf("%q: %q %v", p, got, e)
		}
	}
	for _, p := range []string{"", ".", "../x", "/etc/x", "a/../../b", ".claude/settings.json", ".mcp.json", ".venv/bin/python", "a\\b", "x\x00y", strings.Repeat("a", 256), strings.Repeat("a/", 512) + "x", "./.codex/config"} {
		if _, e := CleanPath(p); e == nil || !strings.HasPrefix(e.Error(), "path ") {
			t.Errorf("accepted %q: %v", p, e)
		}
	}
	for _, f := range []string{".claude", ".codex", ".cursor", ".agents", ".cli-home", ".mcp.json", ".lyk-mcp.json", ".venv", ".lyk", ".lyk-skills", ".no-lyk-config"} {
		if _, e := CleanPath(f + "/x"); e == nil {
			t.Errorf("accepted %q", f)
		}
	}
}
func TestIsText(t *testing.T) {
	for _, tc := range []struct {
		b    []byte
		want bool
	}{{[]byte("hello λ"), true}, {nil, true}, {[]byte{0}, false}, {[]byte{255}, false}, {append([]byte(strings.Repeat("a", 8192)), 0), true}} {
		if got := IsText(tc.b); got != tc.want {
			t.Errorf("got %v want %v", got, tc.want)
		}
	}
}
func TestBlobKey(t *testing.T) {
	ws := uuid.New()
	if got := BlobKey(ws, "abc"); got != ws.String()+"/coresearch/abc" {
		t.Fatal(got)
	}
}

func TestErrStaleMessage(t *testing.T) {
	if got := (ErrStale{Base: 1, Current: 2}).Error(); got != "version 1 is not the latest (2 is); save with force to override" {
		t.Fatal(got)
	}
}

func TestCleanPathProtectsCaseAliasesAndGit(t *testing.T) {
	for _, p := range []string{".CLAUDE/settings.json", ".LyK/config.json", "./.CoDeX/config", ".GiT", "data/.GIT/config", "a/.git"} {
		if _, err := CleanPath(p); err == nil {
			t.Errorf("accepted protected path %q", p)
		}
	}
	for _, p := range []string{".gitignore", ".github/workflows/test.yml", "data/science.csv"} {
		if _, err := CleanPath(p); err != nil {
			t.Errorf("rejected safe path %q: %v", p, err)
		}
	}
}

func TestAttachmentTextHeadAcrossUTF8Boundary(t *testing.T) {
	text := []byte(strings.Repeat("a", 8191) + "λ")
	if !IsText(text) || !IsText(text[:8192]) {
		t.Fatal("a split UTF-8 rune made a text prefix look binary")
	}
}
