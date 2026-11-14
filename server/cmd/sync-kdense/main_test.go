package main

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lykeion/server/internal/science"
)

// checkout writes files and links into a fresh git repository and commits
// them, as a K-Dense checkout would be.
func checkout(t *testing.T, files, links map[string]string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	for p, target := range links {
		os.Symlink(target, filepath.Join(dir, filepath.FromSlash(p)))
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@x", "-c", "user.name=t", "commit", "-q", "-m", "x"}} {
		if b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	head, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	return dir, strings.TrimSpace(string(head))
}

func skill(name, licence string) string {
	l := ""
	if licence != "" {
		l = "license: " + licence + "\n"
	}
	return "---\nname: " + name + "\ndescription: >\n  Does " + name + " things. More words.\n" + l + "---\n\n# " + name + "\n"
}

var upstream = map[string]string{
	"LICENSE.md":                                   "MIT License\n\nCopyright (c) 2025 K-Dense Inc.\n",
	"docs/skills.md":                               "# Scientific Skills\n\n## Scientific Databases & Data Access\n\n- **[DB](../skills/database-lookup/SKILL.md)** - x\n\n## Scientific Packages\n\n### Bioinformatics & Genomics\n\n- **[A](../skills/alpha/SKILL.md)** - x\n- **[G](../skills/gone/SKILL.md)** - x\n- **[F](../skills/fresh/SKILL.md)** - x\n",
	"skills/alpha/SKILL.md":                        skill("alpha", "MIT"),
	"skills/alpha/scripts/_helper.py":              "print('never run')\n",
	"skills/database-lookup/SKILL.md":              skill("database-lookup", "MIT"),
	"skills/database-lookup/references/uniprot.md": "uniprot",
	"skills/database-lookup/references/fred.md":    "fred",
	"skills/gone/SKILL.md":                         skill("gone", "PolyForm-Noncommercial-1.0.0"),
	"skills/fresh/SKILL.md":                        skill("fresh", "MIT"),
}

var cur = curation{Cut: map[string]string{"gone": "licence: non-commercial"}, Allow: []string{"alpha", "database-lookup"},
	Keep:     map[string][]string{"database-lookup": {"uniprot"}},
	Licences: science.LicenceTable{"alpha": {Licence: "MIT", Stated: "MIT", Source: "skills/alpha/LICENSE"}, "database-lookup": {Licence: "MIT", Stated: "MIT", Source: "skills/database-lookup/LICENSE"}}}

func TestSyncCopiesTheCuratedSkillsOnly(t *testing.T) {
	src, head := checkout(t, upstream, nil)
	dst := filepath.Join(t.TempDir(), "bundle")
	var out bytes.Buffer
	if err := run(src, head, dst, cur, &out); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(dst, filepath.FromSlash(p)))
		return string(b)
	}
	if read("alpha/scripts/_helper.py") == "" || read("database-lookup/references/uniprot.md") != "uniprot" {
		t.Fatal("a bundled file is missing")
	}
	for _, p := range []string{"gone", "fresh", "database-lookup/references/fred.md"} {
		if _, err := os.Stat(filepath.Join(dst, p)); err == nil {
			t.Errorf("%s was bundled", p)
		}
	}
	_, body, err := science.ParseFrontmatter([]byte(read("database-lookup/SKILL.md")))
	if err != nil || !strings.HasPrefix(string(body), "\n"+science.Overlays["database-lookup"]+"\n# database-lookup") {
		t.Errorf("the overlay is not placed first: %v %q", err, body)
	}
	var idx []science.Skill
	json.Unmarshal([]byte(read("index.json")), &idx)
	if len(idx) != 2 || idx[0] != (science.Skill{Name: "alpha", Domain: "Bioinformatics & Genomics", Description: "Does alpha things. More words.\n", Licence: "MIT", Stated: "MIT", Source: "skills/alpha/LICENSE"}) ||
		idx[1].Domain != "Scientific Databases & Data Access" {
		t.Fatalf("index: %+v", idx)
	}
	notice := read("NOTICE")
	for _, want := range []string{head, "Copyright (c) 2025 K-Dense Inc.", "\n  alpha  MIT  ", "\n  gone  licence: non-commercial"} {
		if !strings.Contains(notice, want) {
			t.Errorf("NOTICE lacks %q:\n%s", want, notice)
		}
	}
	if !strings.Contains(out.String(), "new upstream skill fresh") || !strings.Contains(out.String(), "+ alpha/SKILL.md") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestSyncRefusesAndChangesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(files, links map[string]string)
		want string
	}{
		{"a licence the rule refuses", func(f, _ map[string]string) { f["skills/alpha/SKILL.md"] = skill("alpha", "CC BY-NC-SA 4.0") }, `alpha: licence "CC BY-NC-SA 4.0"`},
		{"no licence stated is never MIT", func(f, _ map[string]string) { f["skills/alpha/SKILL.md"] = skill("alpha", "") }, `alpha: licence ""`},
		{"a skill under another name", func(f, _ map[string]string) { f["skills/alpha/SKILL.md"] = skill("beta", "MIT") }, `names itself "beta"`},
		{"an allowed skill gone upstream", func(f, _ map[string]string) {
			delete(f, "skills/alpha/SKILL.md")
			delete(f, "skills/alpha/scripts/_helper.py")
		}, "alpha: allowed but missing"},
		{"a connector without its reference", func(f, _ map[string]string) { delete(f, "skills/database-lookup/references/uniprot.md") }, "no reference file for uniprot"},
		{"a link inside a skill", func(_, l map[string]string) { l["skills/alpha/hosts"] = "/etc/hosts" }, "hosts is not a regular file"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files, links := maps.Clone(upstream), map[string]string{}
			c.edit(files, links)
			src, head := checkout(t, files, links)
			dst := filepath.Join(t.TempDir(), "bundle")
			os.MkdirAll(dst, 0o755)
			os.WriteFile(filepath.Join(dst, "keep"), []byte("old"), 0o644)
			err := run(src, head, dst, cur, io.Discard)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want %q", err, c.want)
			}
			if b, _ := os.ReadFile(filepath.Join(dst, "keep")); string(b) != "old" {
				t.Fatal("a refused sync changed the bundle")
			}
		})
	}
	src, _ := checkout(t, upstream, nil)
	if err := run(src, strings.Repeat("0", 40), t.TempDir(), cur, io.Discard); err == nil || !strings.Contains(err.Error(), "not "+strings.Repeat("0", 40)) {
		t.Fatalf("a checkout at another commit: %v", err)
	}
}
