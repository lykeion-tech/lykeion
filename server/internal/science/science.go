package science

import (
	"archive/tar"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"
)

// The curated skill folders, index.json and NOTICE, as cmd/sync-kdense wrote
// them. The all: prefix keeps files such as _common.py that embed would
// otherwise leave out.
//
//go:embed all:bundle
var bundle embed.FS

var skills = sync.OnceValue(func() []Skill {
	b, err := bundle.ReadFile("bundle/index.json")
	if err != nil {
		panic("science: the bundle has no index.json; run cmd/sync-kdense")
	}
	var out []Skill
	if err := json.Unmarshal(b, &out); err != nil {
		panic("science: index.json: " + err.Error())
	}
	return out
})

// Skills lists the bundle, sorted by name.
func Skills() []Skill { return skills() }

func Has(name string) bool {
	return slices.ContainsFunc(Skills(), func(s Skill) bool { return s.Name == name })
}

// Closest names the n bundled skills nearest to name by edit distance.
func Closest(name string, n int) []string {
	name = strings.ToLower(name[:min(len(name), 64)])
	type scored struct {
		name string
		d    int
	}
	var all []scored
	for _, s := range Skills() {
		all = append(all, scored{s.Name, distance(name, s.Name)})
	}
	slices.SortStableFunc(all, func(a, b scored) int { return a.d - b.d })
	var out []string
	for _, s := range all[:min(n, len(all))] {
		out = append(out, s.name)
	}
	return out
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// File reads one file of the bundle, such as NOTICE.
func File(p string) ([]byte, error) { return bundle.ReadFile("bundle/" + p) }

var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Tar writes a bundled skill's folder as a tar stream whose paths are
// relative to the folder.
func Tar(w io.Writer, name string) error {
	if !Has(name) {
		return fs.ErrNotExist
	}
	root := "bundle/" + name
	tw := tar.NewWriter(w)
	err := fs.WalkDir(bundle, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: 0o755, ModTime: epoch})
		}
		b, err := bundle.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: 0o644, Size: int64(len(b)), ModTime: epoch}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// Section is one `##` section of a skill's SKILL.md body. The text after the
// heading line excludes the heading itself.
type Section struct{ Heading, Text string }

// noHeadingsMax bounds the single section of a skill that has no headings.
const noHeadingsMax = 2000

// Sections splits a bundled skill's SKILL.md body on its `##` headings.
func Sections(name string) ([]Section, error) {
	if !Has(name) {
		return nil, fs.ErrNotExist
	}
	b, err := File(name + "/SKILL.md")
	if err != nil {
		return nil, err
	}
	_, body, err := ParseFrontmatter(b)
	if err != nil {
		return nil, err
	}
	return splitSections(string(body)), nil
}

// splitSections cuts body at lines that start with "## ". Text before the
// first heading is a section with an empty heading; a body with no headings
// is one such section of at most noHeadingsMax bytes.
func splitSections(body string) []Section {
	if !strings.HasPrefix(body, "## ") && !strings.Contains(body, "\n## ") {
		return []Section{{Text: strings.ToValidUTF8(body[:min(len(body), noHeadingsMax)], "")}}
	}
	var out []Section
	cur := Section{}
	for _, line := range strings.SplitAfter(body, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			if cur.Heading != "" || strings.TrimSpace(cur.Text) != "" {
				out = append(out, cur)
			}
			cur = Section{Heading: strings.TrimSpace(h)}
			continue
		}
		cur.Text += line
	}
	return append(out, cur)
}
