package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lykeion/server/internal/science"
)

const taskTOML = `[task]
name = "terminal-bench-science/protein-fold"
description = "Fold: a small protein # quickly."
authors = ["Ada Lovelace <ada-secret@example.org>"]

[metadata]
author_name = ["Ada Lovelace", "Grace Hopper"]
author_email = ["ada-secret@example.org", "grace-secret@example.org"]
author_organization = ["Analytical Engines", "Navy"]
author_profile = ["https://profile.example.org/secret-ada"]
domain = "life-sciences"
field = "biology"
subfield = "structural"
tags = ["folding", "python"]
expert_time_estimate_hours = 2.5
relevant_experience = "PhD in structural biology"

[agent]
timeout_sec = 900.0

[environment]
cpus = 2
memory_mb = 4096
`

const instruction = "Fold the protein in /app/data.\n\nWrite the result to /app/out.\n"

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=t@x", "-c", "user.name=t"}, args...)
	if b, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a checkout at tag v0.1.0 with one released task and one
// task the dataset does not list.
func fixture(t *testing.T, extra map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	base := "tasks/life-sciences/biology/protein-fold/"
	put(t, dir, base+"task.toml", taskTOML)
	put(t, dir, base+"instruction.md", instruction)
	put(t, dir, base+"README.md", "readme")
	put(t, dir, base+"solution/solve.sh", "echo solve")
	put(t, dir, base+"environment/Dockerfile", "FROM scratch")
	put(t, dir, base+"tests/test.sh", "echo test")
	other := "tasks/life-sciences/biology/unlisted/"
	put(t, dir, other+"task.toml", strings.ReplaceAll(taskTOML, "protein-fold", "unlisted"))
	put(t, dir, other+"instruction.md", "x")
	put(t, dir, "tasks/dataset.toml", "[[dataset.authors]]\nname = \"x\"\n\n[[tasks]]\nname = \"terminal-bench-science/protein-fold\"\n")
	for p, b := range extra {
		put(t, dir, base+p, b)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "x")
	git(t, dir, "tag", "v0.1.0")
	return dir
}

func TestSyncWritesTheReleasedWorkflowsOnly(t *testing.T) {
	src, dst := fixture(t, nil), filepath.Join(t.TempDir(), "bundle")
	if err := run(src, "v0.1.0", dst, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "unlisted")); err == nil {
		t.Fatal("the unlisted task was bundled")
	}
	b, _ := os.ReadFile(filepath.Join(dst, "index.json"))
	var idx struct {
		Release   string
		Workflows []struct {
			Slug, Name, Domain, Field, Subfield, Description, Experience string
			Tags                                                         []string
			Authors                                                      []struct{ Name, Organization string }
			Expert_hours                                                 float64 `json:"expert_hours"`
			Budget                                                       float64 `json:"agent_budget_sec"`
			Cpus                                                         int     `json:"cpus"`
			Mem                                                          int     `json:"memory_mb"`
		}
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	if idx.Release != "v0.1.0" || len(idx.Workflows) != 1 {
		t.Fatalf("index = %s", b)
	}
	w := idx.Workflows[0]
	if w.Slug != "protein-fold" || w.Name != "Protein fold" || w.Domain != "life-sciences" || w.Field != "biology" ||
		w.Subfield != "structural" || w.Description != "Fold: a small protein # quickly." || w.Experience != "PhD in structural biology" ||
		w.Expert_hours != 2.5 || w.Budget != 900 || w.Cpus != 2 || w.Mem != 4096 ||
		len(w.Tags) != 2 || len(w.Authors) != 2 || w.Authors[1].Name != "Grace Hopper" || w.Authors[1].Organization != "Navy" {
		t.Fatalf("workflow = %+v", w)
	}
	filepath.WalkDir(dst, func(p string, d os.DirEntry, _ error) error {
		if d.Type().IsRegular() {
			body, _ := os.ReadFile(p)
			if bytes.Contains(body, []byte("secret")) {
				t.Errorf("%s leaks an author email or profile", p)
			}
		}
		return nil
	})
	want := "---\nname: workflow-protein-fold\ndescription: \"Fold: a small protein # quickly.\"\n---\n\n> This workflow is a worked example written for the Terminal-Bench-Science benchmark's own data. The paths, files and dates below belong to that example. Apply its method to this study's own data and question. `references/` holds the example's README, reference solution and environment recipe.\n\n" + instruction
	got, _ := os.ReadFile(filepath.Join(dst, "protein-fold", "skill", "SKILL.md"))
	if string(got) != want {
		t.Fatalf("SKILL.md = %q", got)
	}
	fm, _, err := science.ParseFrontmatter(got)
	if err != nil || fm.Description != "Fold: a small protein # quickly." {
		t.Fatalf("frontmatter = %+v, %v", fm, err)
	}
	for _, p := range []string{"skill/references/solution/solve.sh", "skill/references/README.md", "skill/references/task.toml", "skill/references/environment/Dockerfile", "task/tests/test.sh"} {
		if _, err := os.Stat(filepath.Join(dst, "protein-fold", filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	filepath.WalkDir(filepath.Join(dst, "protein-fold", "skill"), func(p string, d os.DirEntry, _ error) error {
		rel := filepath.ToSlash(p)
		if strings.Contains(rel, "tests/") || strings.HasSuffix(rel, "references/instruction.md") {
			t.Errorf("skill holds %s", rel)
		}
		return nil
	})
}

func TestSyncListsButDoesNotEmbedBinaryOrLargeFiles(t *testing.T) {
	src := fixture(t, map[string]string{"environment/blob.py": "a\x00b", "environment/latin.py": "\xff\xfe", "environment/big.py": strings.Repeat("a", 256<<10+1), "environment/data.csv": "a,b"})
	dst := filepath.Join(t.TempDir(), "bundle")
	if err := run(src, "v0.1.0", dst, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "index.json"))
	var idx struct {
		Workflows []struct {
			Files []struct {
				Path     string
				Size     int64
				Embedded bool
			}
		}
	}
	json.Unmarshal(b, &idx)
	sizes := map[string]int64{"environment/blob.py": 3, "environment/latin.py": 2, "environment/big.py": 256<<10 + 1, "environment/data.csv": 3}
	seen := 0
	for _, f := range idx.Workflows[0].Files {
		if size, ok := sizes[f.Path]; ok {
			seen++
			if f.Embedded || f.Size != size {
				t.Errorf("%s = %+v", f.Path, f)
			}
			for _, root := range []string{"task", "skill/references"} {
				if _, err := os.Stat(filepath.Join(dst, "protein-fold", root, filepath.FromSlash(f.Path))); err == nil {
					t.Errorf("%s embedded under %s", f.Path, root)
				}
			}
		} else if !f.Embedded {
			t.Errorf("%s should be embedded", f.Path)
		}
	}
	if seen != 4 {
		t.Fatalf("listed %d of the 4 unembedded files", seen)
	}
}

func TestSyncRefusesAnUntaggedCheckout(t *testing.T) {
	src := fixture(t, nil)
	dst := filepath.Join(t.TempDir(), "bundle")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "keep"), []byte("old"), 0o644)
	put(t, src, "later.txt", "x")
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "later")
	err := run(src, "v0.1.0", dst, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "the checkout is not at v0.1.0") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "keep")); string(b) != "old" {
		t.Fatal("dst changed")
	}
	if _, err := os.Stat(dst + ".new"); err == nil {
		t.Fatal("temp folder left behind")
	}
}
