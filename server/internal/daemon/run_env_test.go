package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunEnvPutsResolvedLykFirstWithoutStudyPaths(t *testing.T) {
	self, _ := os.Executable()
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runPath()
	if err != nil {
		t.Fatal(err)
	}
	first, rest, _ := strings.Cut(got, string(os.PathListSeparator))
	if first != filepath.Dir(resolved) {
		t.Fatalf("first PATH entry %q, want %q", first, filepath.Dir(resolved))
	}
	if rest != os.Getenv("PATH") {
		t.Fatalf("the person's PATH was altered: %q", rest)
	}
}

func TestLykDirFollowsSymlinks(t *testing.T) {
	real, links := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(real, "lyk"), nil, 0o755))
	must(t, os.Symlink(filepath.Join(real, "lyk"), filepath.Join(links, "lyk")))
	want, _ := filepath.EvalSymlinks(real)
	if got, err := resolvedDir(filepath.Join(links, "lyk")); err != nil || got != want {
		t.Fatalf("%q %v, want %q", got, err, want)
	}
}

func TestHandInRunEnvHasNoLyk(t *testing.T) {
	has := func(env []string, prefix string) bool {
		return slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, prefix) })
	}
	lykd, _ := lykDir()
	alias := filepath.Join(t.TempDir(), "alias")
	must(t, os.Symlink(lykd, alias))
	t.Setenv("PATH", lykd+string(os.PathListSeparator)+alias+string(os.PathListSeparator)+"/usr/bin")
	hand, err := cliEnv(true, "ltk_x", "http://s", t.TempDir())
	must(t, err)
	for _, k := range []string{"LYK_TOKEN", "LYK_SERVER", "LYK_CONFIG", "LYK_DOOR"} {
		if has(hand, k) {
			t.Fatalf("a hand-in run's env holds %s: %v", k, hand)
		}
	}
	lyk, _ := lykDir()
	if !slices.Contains(hand, "PATH=/usr/bin") {
		t.Fatalf("hand-in PATH %v", hand)
	}
	if got := doorEnv(true, "claude", &Door{Token: "d"}); len(got) != 0 {
		t.Fatalf("a Claude hand-in run's door env %v", got)
	}
	if got := doorEnv(true, "codex", &Door{Token: "d"}); !slices.Equal(got, []string{"LYK_DOOR_TOKEN=d"}) {
		t.Fatalf("a Codex hand-in run's door env %v", got)
	}
	// A Validation run keeps today's.
	val, err := cliEnv(false, "ltk_x", "http://s", "/run")
	must(t, err)
	for _, k := range []string{"LYK_TOKEN=ltk_x", "LYK_SERVER=http://s", "LYK_CONFIG=/run/.lyk-config.json", "PATH=" + lyk} {
		if !has(val, k) {
			t.Fatalf("a Validation run's env lacks %s: %v", k, val)
		}
	}
	if got := doorEnv(false, "claude", &Door{URL: "u", Token: "d"}); !slices.Equal(got, []string{"LYK_DOOR=u", "LYK_DOOR_TOKEN=d"}) {
		t.Fatalf("a Validation run's door env %v", got)
	}
}
