// Package workflow serves the released Terminal-Bench-Science tasks that
// cmd/sync-tbscience copied into the bundle: one single-agent Workflow per
// task, each with its task files and a ready-made skill folder.
package workflow

import (
	"archive/tar"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// The bundle as cmd/sync-tbscience wrote it. The all: prefix keeps dotfiles
// and underscore files that embed would otherwise leave out.
//
//go:embed all:bundle
var bundle embed.FS

type Author struct {
	Name         string `json:"name"`
	Organization string `json:"organization"`
}

// File is one file of the task folder. Only embedded files can be read.
type File struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Embedded bool   `json:"embedded"`
}

type Workflow struct {
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Domain         string   `json:"domain"`
	Field          string   `json:"field"`
	Subfield       string   `json:"subfield"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	Authors        []Author `json:"authors"`
	Experience     string   `json:"experience"`
	ExpertHours    float64  `json:"expert_hours"`
	AgentBudgetSec float64  `json:"agent_budget_sec"`
	CPUs           int      `json:"cpus"`
	MemoryMB       int      `json:"memory_mb"`
	Files          []File   `json:"files"`
}

type index struct {
	Release   string     `json:"release"`
	Workflows []Workflow `json:"workflows"`
}

var loaded = sync.OnceValue(func() index {
	b, err := bundle.ReadFile("bundle/index.json")
	if err != nil {
		panic("workflow: the bundle has no index.json; run cmd/sync-tbscience")
	}
	var ix index
	if err := json.Unmarshal(b, &ix); err != nil {
		panic("workflow: index.json: " + err.Error())
	}
	return ix
})

// Release is the Terminal-Bench-Science tag the bundle was taken from.
func Release() string { return loaded().Release }

// All lists the bundled workflows, sorted by slug.
func All() []Workflow { return loaded().Workflows }

func Get(slug string) (Workflow, bool) {
	i, ok := slices.BinarySearchFunc(All(), slug, func(w Workflow, s string) int { return strings.Compare(w.Slug, s) })
	if !ok {
		return Workflow{}, false
	}
	return All()[i], true
}

// ReadFile reads one embedded file of a workflow's task folder. Unknown
// slugs, files left out of the bundle and invalid paths are fs.ErrNotExist.
func ReadFile(slug, p string) ([]byte, error) {
	w, ok := Get(slug)
	if !ok || !fs.ValidPath(p) || !slices.ContainsFunc(w.Files, func(f File) bool { return f.Path == p && f.Embedded }) {
		return nil, fs.ErrNotExist
	}
	return bundle.ReadFile(path.Join("bundle", slug, "task", p))
}

// SkillName is the name of the skill a workflow is installed as.
func SkillName(slug string) string { return "workflow-" + slug }

var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// SkillTar writes a workflow's skill folder as a tar stream whose paths are
// relative to the folder.
func SkillTar(w io.Writer, slug string) error {
	if _, ok := Get(slug); !ok {
		return fs.ErrNotExist
	}
	root := "bundle/" + slug + "/skill"
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
