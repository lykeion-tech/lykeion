package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lykeion/server/internal/science"
	"lykeion/server/internal/skill"
)

type localSkillRoot struct{ path, kind string }
type localSkillSummary struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SourcePath  string `json:"source_path"`
	Provider    string `json:"provider"`
	Root        string `json:"root"`
	FileCount   int    `json:"file_count"`
}

func localSkillRoots(harness, home, codexHome string) ([]localSkillRoot, error) {
	var provider string
	switch harness {
	case "claude":
		provider = filepath.Join(home, ".claude", "skills")
	case "codex":
		codexHome = strings.TrimSpace(codexHome)
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		provider = filepath.Join(codexHome, "skills")
	default:
		return nil, fmt.Errorf("unsupported runtime harness")
	}
	return []localSkillRoot{{provider, "provider"}, {filepath.Join(home, ".agents", "skills"), "universal"}}, nil
}

func localSkillKey(key string) bool {
	return fs.ValidPath(key) && key != "." && !strings.ContainsAny(key, "\\\x00:")
}

// The directory handle pins the selected root, including a symlink installed by
// the owner. All child reads are relative to that handle and cannot escape it.
func readPinnedSkill(dir string) (skill.Bundle, error) {
	b := skill.Bundle{Name: filepath.Base(dir), Files: map[string][]byte{}}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return b, err
	}
	defer root.Close()
	total := 0
	var walk func(string, []fs.FileInfo) error
	walk = func(rel string, ancestors []fs.FileInfo) error {
		info, err := root.Stat(rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			for _, previous := range ancestors {
				if os.SameFile(previous, info) {
					return fmt.Errorf("cyclic skill directory")
				}
			}
			f, err := root.Open(rel)
			if err != nil {
				return err
			}
			entries, err := f.ReadDir(-1)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, e := range entries {
				if err := walk(filepath.Join(rel, e.Name()), append(ancestors, info)); err != nil {
					return err
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains a non-regular file")
		}
		if info.Size() > skill.FileBytes {
			return fmt.Errorf("skill file exceeds size limit")
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		actual, err := f.Stat()
		if err != nil || !actual.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("skill file changed while reading")
		}
		data, err := io.ReadAll(io.LimitReader(f, skill.FileBytes+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > skill.FileBytes {
			return fmt.Errorf("skill file exceeds size limit")
		}
		name := filepath.ToSlash(rel)
		if name != "SKILL.md" {
			total += len(data)
		}
		if len(b.Files) >= skill.SupportingFiles+1 || total > skill.SupportingBytes {
			return fmt.Errorf("skill exceeds supporting file limits")
		}
		b.Files[name] = data
		return nil
	}
	if err = walk(".", nil); err != nil {
		return b, err
	}
	if main := b.Files["SKILL.md"]; bytes.HasPrefix(main, []byte("---")) {
		fm, _, err := science.ParseFrontmatter(main)
		if err != nil {
			return b, err
		}
		if fm.Name != "" {
			b.Name = fm.Name
		}
		b.Description = fm.Description
	}
	return b, skill.Validate(b)
}

func skillSourcePath(home, dir string) string {
	rel, err := filepath.Rel(home, dir)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(dir)
}

func loadLocalSkill(harness, home, codexHome, key string) (skill.Bundle, localSkillSummary, error) {
	if !localSkillKey(key) {
		return skill.Bundle{}, localSkillSummary{}, fmt.Errorf("invalid inventory key")
	}
	roots, err := localSkillRoots(harness, home, codexHome)
	if err != nil {
		return skill.Bundle{}, localSkillSummary{}, err
	}
	for _, root := range roots {
		dir := filepath.Join(root.path, filepath.FromSlash(key))
		info, err := os.Stat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return skill.Bundle{}, localSkillSummary{}, err
		}
		if !info.IsDir() {
			continue
		}
		if _, err = os.Stat(filepath.Join(dir, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return skill.Bundle{}, localSkillSummary{}, err
		}
		b, err := readPinnedSkill(dir)
		summary := localSkillSummary{Key: key, Name: b.Name, Description: b.Description, SourcePath: skillSourcePath(home, dir), Provider: harness, Root: root.kind, FileCount: len(b.Files)}
		return b, summary, err
	}
	return skill.Bundle{}, localSkillSummary{}, fmt.Errorf("local skill not found")
}

func listLocalSkills(harness, home, codexHome string) ([]localSkillSummary, error) {
	roots, err := localSkillRoots(harness, home, codexHome)
	if err != nil {
		return nil, err
	}
	out := []localSkillSummary{}
	seen := map[string]bool{}
	for _, root := range roots {
		visited := map[string]bool{}
		var walk func(string, int) error
		walk = func(dir string, depth int) error {
			if depth > 4 {
				return nil
			}
			resolved, err := filepath.EvalSymlinks(dir)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if visited[resolved] {
				return nil
			}
			visited[resolved] = true
			entries, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				child := filepath.Join(dir, entry.Name())
				info, err := os.Stat(child)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if !info.IsDir() {
					continue
				}
				if _, err = os.Stat(filepath.Join(child, "SKILL.md")); err == nil {
					key, err := filepath.Rel(root.path, child)
					if err != nil {
						return err
					}
					key = filepath.ToSlash(key)
					if seen[key] {
						continue
					}
					_, summary, err := loadLocalSkill(harness, home, codexHome, key)
					if err != nil {
						return fmt.Errorf("skill %q: %w", key, err)
					}
					seen[key] = true
					out = append(out, summary)
				} else if errors.Is(err, os.ErrNotExist) {
					if err = walk(child, depth+1); err != nil {
						return err
					}
				} else {
					return err
				}
			}
			return nil
		}
		if err = walk(root.path, 0); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
