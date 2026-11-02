package agent

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// notInRunFolder are the dot-names the adapters and the daemon use that are
// not paths of their own at the top of a run's folder.
var notInRunFolder = map[string]string{
	".catalog-home": "auth-free config home inside a disposable model discovery scratch; never an agent run",
	".fdb_latexmk":  "suffix of untrusted TeX build state removed inside the isolated build source folder",
	".init-":        "initializing build directory prefix inside the daemon-owned build cache",
	".gc-":          "retired build directory prefix inside the daemon-owned build cache",
	".lyk":          "app-owned shared data under personal home",
	".signin-":      "temporary sign-in copy inside the guest CLI config home",
	".ssh":          "personal home credential store", ".aws": "personal home credential store", ".gnupg": "personal home credential store", ".netrc": "personal home credential file", ".docker": "personal home credential store", ".kube": "personal home credential store", ".npmrc": "personal home credential file", ".pypirc": "personal home credential file", ".git-credentials": "personal home credential file", ".caches": "shared cache beside run folders",

	".py": "a script extension", ".r": "a script extension", ".R": "a script extension", ".jl": "a script extension",
	".json": "a file suffix", ".jsonl": "a file suffix", ".tmp": "a file suffix", ".overflow.json": "a file suffix", ".config.toml": "a file suffix",
	".d":      "extracted attachment tree suffix in the daemon state cache",
	".ok":     "attachment verification marker suffix in the daemon state cache",
	".part":   "partial extraction suffix in daemon-owned attachment caches and service folders",
	".tar.gz": "attachment archive filename suffix", ".tar.xz": "attachment archive filename suffix", ".tar.bz2": "attachment archive filename suffix", ".tar.zst": "attachment archive filename suffix",
	".tgz": "attachment archive filename suffix", ".zip": "attachment archive filename suffix", ".tar": "attachment archive filename suffix",
	".cache": "inside .home", ".config": "inside .home", ".local": "inside .home", ".state": "inside .home",
	".credentials.json": "inside .cli-home", ".lyk-usage.json": "inside .cli-home", ".lyk-bin": "inside .cli-home",
	".system_generated":        "inside the CLI's session store",
	".sqlite-maintenance.lock": "inside the account's Codex home, not the run folder",
	".sqlite":                  "a file suffix inside the account's Codex home, not the run folder",
	".scratch-retired-":        "beside the run folders, not in one",
	".ack-":                    "acknowledged Coresearch receipt directory inside protected StateDir/coresearch-receipts, never a run folder",
}

// Every name the adapters and the daemon keep at the top of a run's folder
// is in RunFolderConfig, so a new one can't be left writable to code by
// accident: a new dot-name (or *.md) literal fails here until it is listed
// or explained.
func TestRunFolderConfigListsEveryName(t *testing.T) {
	name := regexp.MustCompile(`^(\.[A-Za-z][\w.-]*|[A-Z][A-Za-z.]*\.md)(/.*)?$`)
	var files []string
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "daemon")} {
		got, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, got...)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := goparser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			m := name.FindStringSubmatch(s)
			if err != nil || m == nil {
				return true
			}
			found++
			listed := slices.ContainsFunc(RunFolderConfig, func(p RunFolderPath) bool { return p.Name == m[1] })
			if _, other := notInRunFolder[m[1]]; !listed && !other {
				t.Errorf("%s: %q is neither in RunFolderConfig nor explained in notInRunFolder", filepath.Base(f), m[1])
			}
			return true
		})
	}
	if found < 30 {
		t.Fatalf("only %d names found; the scan is not reading the sources", found)
	}
	for _, d := range []string{".codex", ".claude", ".agents", ".claude.json", ".mcp.json", "AGENTS.md", "AGENTS.override.md", "CLAUDE.md", "CLAUDE.local.md"} {
		if !slices.ContainsFunc(RunFolderConfig, func(p RunFolderPath) bool { return p.Name == d }) {
			t.Errorf("discovery name %q is not in RunFolderConfig", d)
		}
	}
}

// Names a CLI reads from its working folder by itself, which no source here
// spells out, so the scan above can't see them.
func TestRunFolderConfigHasEachCLIsOwnFiles(t *testing.T) {
	byName := map[string]RunFolderPath{}
	for _, p := range RunFolderConfig {
		byName[p.Name] = p
	}
	for _, n := range []string{".git", ".agents", ".claude", "CLAUDE.md", "AGENTS.md", ".codex"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("%s is not listed", n)
		}
	}
	// What holds the door's token or the CLI's sign-in is hidden, not only
	// read-only.
	for _, n := range []string{".lyk-mcp.json", ".cli-home", ".home", ".lyk-config.json"} {
		if !byName[n].Hidden {
			t.Errorf("%s is not hidden", n)
		}
	}
	// The supervisor writes its exit marker from outside the sandbox: a
	// placeholder, not a marker that may be missing, keeps code from planting
	// a link it would write through.
	for _, n := range []string{".agent-exit", ".agent-exit.tmp"} {
		if byName[n].Marker {
			t.Errorf("%s is left missing for code to plant", n)
		}
	}
}
