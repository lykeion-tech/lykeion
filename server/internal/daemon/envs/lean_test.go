package envs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testLean = "leanprover/lean4:v4.30.0"

func TestLeanNamesAreChecked(t *testing.T) {
	for _, pair := range [][2]string{{"../x", ""}, {"leanprover/lean4:v4;rm", ""}, {testLean, "main;x"}, {testLean, "main"}, {"", "v4.30.0"}} {
		b, f := fakeLeanBases(t)
		if _, err := b.buildLeanForTest(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatalf("accepted %q", pair)
		}
		if len(f.calls) != 0 {
			t.Fatalf("ran before validation: %v", f.calls)
		}
	}
	b, _ := fakeLeanBases(t)
	if _, err := b.buildLeanForTest(context.Background(), testLean, ""); err != nil {
		t.Fatal(err)
	}
}

func TestLeanPairIsBuiltOnceAndShared(t *testing.T) {
	b, f := fakeLeanBases(t)
	ctx := context.Background()
	pair, err := b.buildLeanForTest(ctx, testLean, "")
	if err != nil {
		t.Fatal(err)
	}
	count := len(f.calls)
	if count == 0 {
		t.Fatal("no build")
	}
	for range 2 {
		same, err := b.buildLeanForTest(ctx, testLean, "v4.30.0")
		if err != nil || same != pair || len(f.calls) != count {
			t.Fatalf("not reused: %s %v calls=%v", same, err, f.calls)
		}
	}
	for range 2 {
		s := newStudy(b, t.TempDir())
		before, err := s.Environment(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := envValue(before, "ELAN_HOME"); ok {
			t.Fatal("elan before UseLean")
		}
		if err := s.UseLean(ctx, pair); err != nil {
			t.Fatal(err)
		}
		e, err := s.Environment(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if home, _ := envValue(e, "ELAN_HOME"); home != filepath.Join(pair, "elan") {
			t.Fatal(e)
		}
		if !strings.Contains(s.PathPrefix(), filepath.Join(pair, "elan", "bin")) {
			t.Fatal(s.PathPrefix())
		}
		body, err := os.ReadFile(filepath.Join(s.Dir, "lean", "lakefile.toml"))
		if err != nil || !strings.Contains(string(body), filepath.Join(pair, "mathlib")) {
			t.Fatalf("project: %s %v", body, err)
		}
		manifestBytes, err := os.ReadFile(filepath.Join(s.Dir, "lean", "lake-manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Packages []map[string]any `json:"packages"`
		}
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatal(err)
		}
		if len(manifest.Packages) != len(leanFixtureDeps)+1 {
			t.Fatalf("packages: %s", manifestBytes)
		}
		for i, p := range manifest.Packages {
			name, path, config, inherited := "mathlib", filepath.Join(pair, "mathlib"), "lakefile.lean", false
			if i < len(leanFixtureDeps) {
				name, inherited, config = leanFixtureDeps[i][0], true, "lakefile.toml"
				path = filepath.Join(pair, "mathlib", ".lake", "packages", name)
			}
			if p["name"] != name || p["type"] != "path" || p["dir"] != path || !filepath.IsAbs(path) || p["inherited"] != inherited || p["configFile"] != config || p["manifestFile"] != "lake-manifest.json" {
				t.Fatalf("package %d: %v", i, p)
			}
			for _, field := range []string{"url", "rev", "inputRev", "subDir"} {
				if _, ok := p[field]; ok {
					t.Fatalf("git locator %s retained: %v", field, p)
				}
			}
		}
		next := newStudy(b, s.Dir)
		e, err = next.Environment(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if home, _ := envValue(e, "ELAN_HOME"); home != filepath.Join(pair, "elan") {
			t.Fatalf("lost selection: %v", e)
		}
	}
}

func TestLeanIsBuiltOutsideTheStudySandbox(t *testing.T) {
	b, f := fakeLeanBases(t)
	s := ForStudy(b, t.TempDir())
	s.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		t.Fatal("study runner used to build trusted base")
		return nil, nil
	}
	if _, err := s.Bases.buildLeanForTest(context.Background(), testLean, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) == 0 {
		t.Fatal("daemon runner not used")
	}
}

func TestAFailedLeanBuildLeavesNoReadyMarker(t *testing.T) {
	b, _ := fakeLeanBases(t)
	b.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		return nil, errors.New("failed download")
	}
	if _, err := b.buildLeanForTest(context.Background(), testLean, ""); err == nil {
		t.Fatal("failed build succeeded")
	}
	ready, _ := filepath.Glob(filepath.Join(b.Dir, "lean", "*", "ready"))
	if len(ready) != 0 {
		t.Fatal(ready)
	}
}

func TestLeanRootRejectsSymlinks(t *testing.T) {
	b, _ := fakeLeanBases(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(b.Dir, "lean")); err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureLeanRoot(); err == nil {
		t.Fatal("accepted linked trusted root")
	}
}

var leanFixtureDeps = [][3]string{
	{"plausible", "leanprover-community/plausible", "a456461b368b71d2accd95234832cd9c174b5437"},
	{"LeanSearchClient", "leanprover-community/LeanSearchClient", "c5d5b8fe6e5158def25cd28eb94e4141ad97c843"},
	{"importGraph", "leanprover-community/import-graph", "515cf9d0c00ece5e661f6de4326a53dedc1e8ea1"},
	{"proofwidgets", "leanprover-community/ProofWidgets4", "a84b3e2475d5c5ab979567b1ad8aea21b764bcf8"},
	{"aesop", "leanprover-community/aesop", "558915ae105bfd8074e22d597613d1961822adc2"},
	{"Qq", "leanprover-community/quote4", "a6e6c34c4ef182f83b219a3a5a385f51f44bdc4c"},
	{"batteries", "leanprover-community/batteries", "32dc18cde3684679f3c003de608743b57498c56f"},
	{"Cli", "leanprover/lean4-cli", "6b907cf12b2e445ccb7c24bc208ef04a1f39e84c"},
}

func leanFixtureManifest() map[string]any {
	packages := []map[string]any{}
	for _, p := range leanFixtureDeps {
		packages = append(packages, map[string]any{"name": p[0], "url": "https://github.com/" + p[1], "rev": p[2], "type": "git", "subDir": nil, "inputRev": "main", "configFile": "lakefile.toml", "manifestFile": "lake-manifest.json"})
	}
	return map[string]any{"version": "1.2.0", "packagesDir": ".lake/packages", "lakeDir": ".lake", "packages": packages}
}

type leanBuildFixture struct {
	*fakeRun
	manifest      map[string]any
	fetched       map[string]string
	wrongCheckout bool
	lakeCalls     int
	gitCalls      int
}

func fakeLeanBases(t *testing.T) (*Bases, *leanBuildFixture) {
	b, f := fakeBases(t)
	fixture := &leanBuildFixture{fakeRun: f, manifest: leanFixtureManifest(), fetched: map[string]string{}}
	b.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if filepath.Base(name) == "git" {
			fixture.gitCalls++
			switch args[0] {
			case "clone":
				path := args[len(args)-1]
				if err := os.MkdirAll(path, 0755); err != nil {
					return nil, err
				}
				body, _ := json.Marshal(fixture.manifest)
				if err := os.WriteFile(filepath.Join(path, "lake-manifest.json"), body, 0644); err != nil {
					return nil, err
				}
			case "fetch":
				fixture.fetched[dir] = args[len(args)-1]
			case "rev-parse":
				sha := fixture.fetched[dir]
				if sha == "" {
					sha = strings.Repeat("a", 40)
				}
				if fixture.wrongCheckout && fixture.lakeCalls > 0 && filepath.Base(dir) == "aesop" {
					sha = strings.Repeat("b", 40)
				}
				f.calls = append(f.calls, "git "+strings.Join(args, " "))
				f.envs = append(f.envs, env)
				return []byte(sha + "\n"), nil
			}
		}
		if filepath.Base(name) == "lake" {
			fixture.lakeCalls++
		}
		return f.run(ctx, env, dir, name, args...)
	}
	return b, fixture
}

func TestMathlibDependenciesAreChecked(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"approved", func(map[string]any) {}},
		{"unknown", func(m map[string]any) { m["url"] = "https://github.com/example/unknown" }},
		{"path", func(m map[string]any) { m["type"] = "path"; m["dir"] = "/tmp/untrusted" }},
		{"branch", func(m map[string]any) { m["rev"] = "main" }},
		{"http", func(m map[string]any) { m["url"] = "http://github.com/leanprover-community/aesop" }},
		{"lookalike", func(m map[string]any) { m["url"] = "https://github.com/leanprover-communty/aesop" }},
		{"name-escape", func(m map[string]any) { m["name"] = "../escape" }},
		{"config-escape", func(m map[string]any) { m["configFile"] = "../../outside.lean" }},
		{"subdir", func(m map[string]any) { m["subDir"] = "../outside" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, f := fakeLeanBases(t)
			dep := f.manifest["packages"].([]map[string]any)[0]
			tt.change(dep)
			_, err := b.buildLeanForTest(context.Background(), testLean, "")
			if tt.name == "approved" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			expected := fmt.Sprintf("Mathlib v4.30.0 needs %s, which Lean builds don't fetch", dep["url"])
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("want %q, got %v", expected, err)
			}
			if f.lakeCalls != 0 || len(f.fetched) != 0 {
				t.Fatalf("untrusted dependency reached builds: lake=%d dependencies=%v", f.lakeCalls, f.fetched)
			}
		})
	}
}

func TestLeanBuildsIgnorePersonalGit(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "url.https://example.invalid/.insteadOf", "GIT_CONFIG_VALUE_0": "https://github.com/", "GIT_CONFIG_PARAMETERS": "personal override", "ELAN_DIST_SERVER": "https://example.invalid", "LEAN_PATH": "/personal"} {
		t.Setenv(k, v)
	}
	b, f := fakeLeanBases(t)
	pair, err := b.buildLeanForTest(context.Background(), testLean, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, call := range f.calls {
		if !strings.HasPrefix(call, "git ") && !strings.HasPrefix(call, "lake ") {
			continue
		}
		env := f.envs[i]
		for k, want := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0", "HOME": filepath.Join(pair, "home")} {
			if got, _ := envValue(env, k); got != want {
				t.Errorf("%s %s=%q want %q", call, k, got, want)
			}
		}
		for _, k := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS", "ELAN_DIST_SERVER", "LEAN_PATH"} {
			if got, ok := envValue(env, k); ok {
				t.Errorf("inherited %s=%q", k, got)
			}
		}
	}
	if f.gitCalls == 0 || f.lakeCalls == 0 {
		t.Fatal("no real build sequence recorded")
	}
}

func TestALeanBuildChecksItsCheckouts(t *testing.T) {
	b, f := fakeLeanBases(t)
	f.wrongCheckout = true
	if _, err := b.buildLeanForTest(context.Background(), testLean, ""); err == nil {
		t.Fatal("changed checkout accepted")
	}
	markers, _ := filepath.Glob(filepath.Join(b.Dir, "lean", "*", "ready"))
	if len(markers) != 0 {
		t.Fatal(markers)
	}
}

func TestLeanBuildLockHonorsDeadline(t *testing.T) {
	b, _ := fakeLeanBases(t)
	started, release := make(chan struct{}), make(chan struct{})
	b.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		close(started)
		<-release
		return nil, errors.New("stop")
	}
	done := make(chan struct{})
	go func() { defer close(done); b.buildLeanForTest(context.Background(), testLean, "") }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := b.buildLeanForTest(ctx, testLean, "")
	close(release)
	<-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestLeanBaseRunnerCancellationStopsChildren(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("supported Lean platform required")
	}
	dir := t.TempDir()
	b := NewBases(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := b.Run(ctx, nil, dir, "/bin/sh", "-c", `touch started; (sleep 0.4; touch escaped) & wait`)
		done <- err
	}()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); err != nil {
		cancel()
		<-done
		t.Fatal("build never started", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled build succeeded")
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("build child survived cancellation: %v", err)
	}
}
