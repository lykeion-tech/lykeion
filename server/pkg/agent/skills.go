package agent

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// skillFolders says where each harness reads project skills, relative to the
// run's folder.
var skillFolders = map[string]string{
	"claude":      ".claude/skills",
	"codex":       ".agents/skills",
	"cursor":      ".cursor/skills",
	"antigravity": ".agents/skills",
}

// SkillsDir is the folder a harness reads skills from in a run folder. It is
// always the run's own folder, never the account's config home.
func SkillsDir(harness, dir string) (string, error) {
	rel, ok := skillFolders[harness]
	if !ok {
		return "", fmt.Errorf("no skills folder is known for harness %q", harness)
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// PinnedName is the name a pinned skill is installed under, so it never
// shares a name with one of the account's own skills.
func PinnedName(name string) string { return "lyk-" + name }

// ReloadSkills starts a resumed session's prompt: its working folder is new,
// so skills it unpacked earlier are not on disk.
const ReloadSkills = "This session continues in a new working folder. Skills you loaded earlier are not here: load any you still need again."

// placeSkills wipes the run folder's skills folder and copies the staged
// skill folders into it, so every prepare leaves exactly the staged skills
// there. A private run is given none: its folder must hold no CLI discovery
// paths. Only folders and regular files are copied.
func placeSkills(o *ExecOptions, harness string) error {
	if o.SkillsFrom == "" || !o.Inherit {
		return nil
	}
	es, err := os.ReadDir(o.SkillsFrom)
	if err != nil {
		return err
	}
	dst, err := SkillsDir(harness, o.Dir)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, e := range es {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "lyk-") {
			return fmt.Errorf("%q is not a staged Lykeion skill", e.Name())
		}
		err := filepath.WalkDir(filepath.Join(o.SkillsFrom, e.Name()), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(o.SkillsFrom, p)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			switch {
			case d.IsDir():
				return os.MkdirAll(target, 0o700)
			case d.Type().IsRegular():
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				return os.WriteFile(target, b, 0o600)
			}
			return fmt.Errorf("%q is not a file or a folder", rel)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// RenameSkill sets the name in a skill's SKILL.md frontmatter, which the
// Agent Skills format requires to match the skill's folder.
func RenameSkill(folder, from, to string) error {
	p := filepath.Join(folder, "SKILL.md")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return errors.New("SKILL.md has no frontmatter")
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return errors.New("SKILL.md's frontmatter is not closed")
	}
	head := strings.Split(s[4:4+end], "\n")
	found := false
	for i, l := range head {
		if k, v, ok := strings.Cut(l, ":"); ok && k == "name" && strings.Trim(strings.TrimSpace(v), `"'`) == from {
			head[i], found = "name: "+to, true
			break
		}
	}
	if !found {
		return fmt.Errorf("SKILL.md does not name %q", from)
	}
	return os.WriteFile(p, []byte("---\n"+strings.Join(head, "\n")+s[4+end:]), 0o644)
}

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// ValidSkillName says whether name can be a bundled skill's folder; with the
// "lyk-" prefix it stays within the format's 64 characters.
func ValidSkillName(name string) bool { return skillName.MatchString(name) }

const maxSkillBytes = 64 << 20

// Untar unpacks a skill folder into dst. Only folders and regular files are
// written, every path must stay inside dst, and the whole is capped, so a
// tar from a broken or hostile server cannot write anywhere else.
func Untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(h.Name, "/"))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("%q leaves the skill's folder", h.Name)
		}
		target := filepath.Join(dst, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if h.Size < 0 || total > maxSkillBytes {
				return errors.New("the skill is over 64 MiB")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, h.Size))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("%q is not a file or a folder", h.Name)
		}
	}
}
