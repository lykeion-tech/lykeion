// Command sync-tbscience copies the released Terminal-Bench-Science tasks
// from a local checkout into the server's workflow bundle. It only reads
// the checkout: nothing in it is executed.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

const (
	prefix   = "terminal-bench-science/"
	maxEmbed = 256 << 10
	preamble = "> This workflow is a worked example written for the Terminal-Bench-Science benchmark's own data. The paths, files and dates below belong to that example. Apply its method to this study's own data and question. `references/` holds the example's README, reference solution and environment recipe.\n\n"
)

type author struct {
	Name         string `json:"name"`
	Organization string `json:"organization"`
}

type file struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Embedded bool   `json:"embedded"`
}

type workflow struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Domain      string   `json:"domain"`
	Field       string   `json:"field"`
	Subfield    string   `json:"subfield"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Authors     []author `json:"authors"`
	Experience  string   `json:"experience"`
	ExpertHours float64  `json:"expert_hours"`
	BudgetSec   float64  `json:"agent_budget_sec"`
	CPUs        int      `json:"cpus"`
	MemoryMB    int      `json:"memory_mb"`
	Files       []file   `json:"files"`
}

// taskFile holds the fields of task.toml the bundle uses. Author emails and
// profile links are deliberately not declared, so they are never read.
type taskFile struct {
	Task struct {
		Name        string
		Description string
	}
	Metadata struct {
		AuthorName              any `toml:"author_name"`
		AuthorOrganization      any `toml:"author_organization"`
		Domain                  string
		Field                   string
		Subfield                string
		Tags                    []string
		ExpertTimeEstimateHours float64 `toml:"expert_time_estimate_hours"`
		RelevantExperience      string  `toml:"relevant_experience"`
	}
	Agent struct {
		TimeoutSec float64 `toml:"timeout_sec"`
	}
	Environment struct {
		CPUs     int
		MemoryMB int `toml:"memory_mb"`
	}
}

func main() {
	src := flag.String("src", "", "a local checkout of laude-institute/terminal-bench-science")
	tag := flag.String("tag", "v0.1.0", "the release tag the checkout must be at")
	dst := flag.String("dst", "internal/workflow/bundle", "the bundle folder to replace")
	flag.Parse()
	if err := run(*src, *tag, *dst, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sync-tbscience:", err)
		os.Exit(1)
	}
}

func run(src, tag, dst string, out io.Writer) error {
	if src == "" || tag == "" {
		return errors.New("usage: sync-tbscience -src CHECKOUT [-tag TAG] [-dst BUNDLE]")
	}
	got, err := exec.Command("git", "-C", src, "describe", "--tags", "--exact-match", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(got)) != tag {
		return fmt.Errorf("the checkout is not at %s", tag)
	}
	var ds struct{ Tasks []struct{ Name string } }
	b, err := os.ReadFile(filepath.Join(src, "tasks", "dataset.toml"))
	if err != nil {
		return err
	}
	if err := toml.Unmarshal(b, &ds); err != nil {
		return fmt.Errorf("dataset.toml: %w", err)
	}
	released := map[string]bool{}
	for _, t := range ds.Tasks {
		released[t.Name] = true
	}

	tmp := filepath.Clean(dst) + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	var flows []workflow
	var problems []string
	err = filepath.WalkDir(filepath.Join(src, "tasks"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "task.toml" {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var tt taskFile
		if err := toml.Unmarshal(raw, &tt); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", p, err))
			return nil
		}
		if !released[tt.Task.Name] {
			return nil
		}
		w, err := bundleTask(filepath.Dir(p), tmp, tt)
		if err != nil {
			problems = append(problems, tt.Task.Name+": "+err.Error())
			return nil
		}
		flows = append(flows, w)
		return nil
	})
	if err != nil {
		return err
	}
	if len(flows) != len(released) {
		problems = append(problems, fmt.Sprintf("dataset.toml lists %d tasks, the checkout holds %d of them", len(released), len(flows)))
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("nothing changed; fix these first:\n  %s", strings.Join(problems, "\n  "))
	}

	slices.SortFunc(flows, func(a, b workflow) int { return strings.Compare(a.Slug, b.Slug) })
	idx, err := json.MarshalIndent(map[string]any{"release": tag, "workflows": flows}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "index.json"), append(idx, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Fprintf(out, "bundled %d workflows at %s\n", len(flows), tag)
	return nil
}

// bundleTask writes one task folder's task/ and skill/ trees under tmp.
func bundleTask(dir, tmp string, tt taskFile) (workflow, error) {
	slug := strings.TrimPrefix(tt.Task.Name, prefix)
	m := tt.Metadata
	if !slugRE.MatchString(slug) {
		return workflow{}, fmt.Errorf("slug %q must match %s", slug, slugRE)
	}
	w := workflow{
		Slug: slug, Name: capitalise(strings.ReplaceAll(slug, "-", " ")), Domain: kebab(m.Domain), Field: kebab(m.Field),
		Subfield: kebab(m.Subfield), Description: tt.Task.Description, Tags: append([]string{}, m.Tags...),
		Authors: []author{}, Experience: m.RelevantExperience, ExpertHours: m.ExpertTimeEstimateHours,
		BudgetSec: tt.Agent.TimeoutSec, CPUs: tt.Environment.CPUs, MemoryMB: tt.Environment.MemoryMB, Files: []file{},
	}
	orgs := stringList(m.AuthorOrganization)
	for i, n := range stringList(m.AuthorName) {
		a := author{Name: n}
		if i < len(orgs) {
			a.Organization = orgs[i]
		}
		w.Authors = append(w.Authors, a)
	}
	instruction, err := os.ReadFile(filepath.Join(dir, "instruction.md"))
	if err != nil {
		return w, err
	}
	root := filepath.Join(tmp, slug)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file or folder", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f := file{Path: rel, Size: info.Size()}
		if info.Size() <= maxEmbed && embeddable(rel) {
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if f.Embedded = utf8.Valid(body) && !bytes.Contains(body, []byte{0}); f.Embedded {
				if rel == "task.toml" {
					if body, err = withoutContacts(body); err != nil {
						return err
					}
				}
				if err := write(filepath.Join(root, "task", rel), body); err != nil {
					return err
				}
				if isReference(rel) {
					if err := write(filepath.Join(root, "skill", "references", rel), body); err != nil {
						return err
					}
				}
			}
		}
		w.Files = append(w.Files, f)
		return nil
	})
	if err != nil {
		return w, err
	}
	desc, err := json.Marshal(tt.Task.Description)
	if err != nil {
		return w, err
	}
	skill := "---\nname: workflow-" + slug + "\ndescription: " + string(desc) + "\n---\n\n" + preamble + string(instruction)
	return w, write(filepath.Join(root, "skill", "SKILL.md"), []byte(skill))
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// kebab lowercases a taxonomy value and joins its words with hyphens.
func kebab(v string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(v)), " ", "-")
}

// withoutContacts drops the author emails and profile links from a
// task.toml, so the bundled copies carry no contact details.
func withoutContacts(b []byte) ([]byte, error) {
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if t, ok := m["task"].(map[string]any); ok {
		delete(t, "authors")
	}
	if t, ok := m["metadata"].(map[string]any); ok {
		delete(t, "author_email")
		delete(t, "author_profile")
	}
	return toml.Marshal(m)
}

// stringList reads a task.toml value that is either one string or a list.
func stringList(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// embeddable reports whether a file name is one of the recipe and code files
// the bundle keeps; data files are only listed.
func embeddable(rel string) bool {
	name := path.Base(rel)
	if strings.HasPrefix(name, "Dockerfile") {
		return true
	}
	if ok, _ := path.Match("requirements*.txt", name); ok {
		return true
	}
	return slices.Contains(codeExts, path.Ext(name))
}

var codeExts = strings.Fields(".md .py .sh .R .r .toml .yml .yaml .lock .jl .c .cc .cpp .h .hpp .rs .go .lean .m .f90 .java .js .ts .cfg .ipynb")

// isReference reports whether a task file goes into the skill's references:
// never the tests, and never the instruction, which is the skill body.
func isReference(rel string) bool {
	top, _, _ := strings.Cut(rel, "/")
	if top == "environment" {
		name := path.Base(rel)
		for _, pat := range []string{"Dockerfile*", "requirements*", "*.lock", "docker-compose*", "compose*"} {
			if ok, _ := path.Match(pat, name); ok {
				return true
			}
		}
		return false
	}
	return rel == "README.md" || rel == "task.toml" || top == "solution"
}

func write(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func capitalise(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}
