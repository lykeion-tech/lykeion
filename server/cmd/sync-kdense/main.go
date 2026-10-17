// Command sync-kdense copies the curated K-Dense scientific skills from a
// local checkout into the server's bundle. It only reads the checkout:
// nothing in it is executed.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"lykeion/server/internal/science"
)

type curation struct {
	Cut      map[string]string
	Allow    []string
	Keep     map[string][]string
	Licences science.LicenceTable
}

func main() {
	src := flag.String("src", "", "a local checkout of K-Dense-AI/scientific-agent-skills")
	commit := flag.String("commit", science.Commit, "the commit the checkout must be at")
	dst := flag.String("dst", "internal/science/bundle", "the bundle folder to replace")
	flag.Parse()
	c := curation{Cut: science.Cut, Allow: science.Allow, Keep: science.Keep(), Licences: science.Licences}
	if err := run(*src, *commit, *dst, c, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sync-kdense:", err)
		os.Exit(1)
	}
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func run(src, commit, dst string, c curation, out io.Writer) error {
	if src == "" || !commitRE.MatchString(commit) {
		return errors.New("usage: sync-kdense -src CHECKOUT [-commit SHA] [-dst BUNDLE]")
	}
	head, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("reading the checkout's commit: %w", err)
	}
	if got := strings.TrimSpace(string(head)); got != commit {
		return fmt.Errorf("the checkout is at %s, not %s", got, commit)
	}
	domains, err := readDomains(filepath.Join(src, "docs", "skills.md"))
	if err != nil {
		return err
	}
	licence, err := os.ReadFile(filepath.Join(src, "LICENSE.md"))
	if err != nil {
		return err
	}
	ents, err := os.ReadDir(filepath.Join(src, "skills"))
	if err != nil {
		return err
	}
	tmp := filepath.Clean(dst) + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	var skills []science.Skill
	var problems []string
	found := map[string]bool{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		found[name] = true
		if _, cut := c.Cut[name]; cut {
			continue
		}
		if !slices.Contains(c.Allow, name) {
			fmt.Fprintf(out, "new upstream skill %s: not bundled until it is added to science.Allow or science.Cut\n", name)
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, "skills", name, "SKILL.md"))
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}
		fm, _, err := science.ParseFrontmatter(b)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}
		lic, ok := c.Licences.Licence(name, fm.License)
		switch {
		case fm.Name != name:
			problems = append(problems, fmt.Sprintf("%s: SKILL.md names itself %q", name, fm.Name))
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: licence %q has no checked row in science.Licences, or differs from it (add a row only from the skill's own licence)", name, fm.License))
		case domains[name] == "":
			problems = append(problems, name+": docs/skills.md lists it under no domain")
		}
		if err := copyTree(filepath.Join(src, "skills", name), filepath.Join(tmp, name)); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
		skills = append(skills, science.Skill{Name: name, Domain: domains[name], Description: fm.Description, Licence: lic, Stated: fm.License, Source: c.Licences[name].Source})
	}
	for _, name := range c.Allow {
		if !found[name] {
			problems = append(problems, name+": allowed but missing from the checkout")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Cut)) {
		if !found[name] {
			fmt.Fprintf(out, "cut skill %s is gone upstream: remove it from science.Cut\n", name)
		}
	}
	if len(problems) == 0 {
		for _, skill := range slices.Sorted(maps.Keys(c.Keep)) {
			if err := overlay(filepath.Join(tmp, skill), skill, c.Keep[skill]); err != nil {
				problems = append(problems, skill+": "+err.Error())
			}
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("nothing changed; fix these first:\n  %s", strings.Join(problems, "\n  "))
	}

	slices.SortFunc(skills, func(a, b science.Skill) int { return strings.Compare(a.Name, b.Name) })
	idx, err := json.MarshalIndent(skills, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "index.json"), append(idx, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "NOTICE"), notice(commit, string(licence), skills, c.Cut), 0o644); err != nil {
		return err
	}
	if err := diff(dst, tmp, out); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Fprintf(out, "bundled %d skills at %s; %d cut\n", len(skills), commit, len(c.Cut))
	return nil
}

var (
	heading = regexp.MustCompile(`^#{2,3} (.+)$`)
	link    = regexp.MustCompile(`\.\./skills/([^/]+)/SKILL\.md`)
)

// readDomains maps each skill to the heading it is first listed under in
// the catalogue page.
func readDomains(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out, cur := map[string]string{}, ""
	for _, line := range strings.Split(string(b), "\n") {
		if m := heading.FindStringSubmatch(line); m != nil {
			cur = strings.TrimSpace(m[1])
			continue
		}
		for _, m := range link.FindAllStringSubmatch(line, -1) {
			if _, ok := out[m[1]]; !ok {
				out[m[1]] = cur
			}
		}
	}
	return out, nil
}

// copyTree copies folders and regular files. A link or any other kind of
// file refuses the skill, so nothing outside the checkout is ever read.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, 0o644)
		}
		return fmt.Errorf("%s is not a regular file or folder", filepath.ToSlash(rel))
	})
}

// overlay keeps only the named reference files and places Lykeion's section
// first in the skill's SKILL.md, right after its frontmatter.
func overlay(dir, skill string, keep []string) error {
	refs := filepath.Join(dir, "references")
	es, err := os.ReadDir(refs)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range es {
		n := strings.TrimSuffix(e.Name(), ".md")
		if slices.Contains(keep, n) {
			have[n] = true
			continue
		}
		if err := os.Remove(filepath.Join(refs, e.Name())); err != nil {
			return err
		}
	}
	for _, n := range keep {
		if !have[n] {
			return fmt.Errorf("no reference file for %s", n)
		}
	}
	p := filepath.Join(dir, "SKILL.md")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	_, body, err := science.ParseFrontmatter(b)
	if err != nil {
		return err
	}
	head := b[:len(b)-len(body)]
	return os.WriteFile(p, slices.Concat(head, []byte("\n"+science.Overlays[skill]), body), 0o644)
}

func notice(commit, licence string, skills []science.Skill, cut map[string]string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "Lykeion bundles skills from K-Dense-AI scientific-agent-skills\nhttps://github.com/K-Dense-AI/scientific-agent-skills\ncommit %s\n\n", commit)
	b.WriteString("The repository's licence:\n\n" + strings.TrimSpace(licence) + "\n\n")
	b.WriteString("Lykeion placed a section of its own first in the SKILL.md of database-lookup and paper-lookup, and kept their\nreference files only for the databases Lykeion connects to. No other file was changed.\n\n")
	stated := func(s science.Skill) string {
		if s.Stated == "" {
			return "no licence stated in SKILL.md; established from " + s.Source
		}
		return fmt.Sprintf("stated: %q", s.Stated)
	}
	fmt.Fprintf(&b, "Bundled skills (%d), each under the licence its SKILL.md states:\n", len(skills))
	for _, s := range skills {
		fmt.Fprintf(&b, "  %s  %s  (%s)\n", s.Name, s.Licence, stated(s))
	}
	fmt.Fprintf(&b, "\nNot bundled (%d):\n", len(cut))
	for _, n := range slices.Sorted(maps.Keys(cut)) {
		fmt.Fprintf(&b, "  %s  %s\n", n, cut[n])
	}
	return []byte(b.String())
}

// diff prints each file the sync adds (+), removes (-) or changes (~).
func diff(old, next string, out io.Writer) error {
	a, err := sums(old)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	b, err := sums(next)
	if err != nil {
		return err
	}
	var lines []string
	for p, s := range b {
		switch a[p] {
		case "":
			lines = append(lines, "+ "+p)
		case s:
		default:
			lines = append(lines, "~ "+p)
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			lines = append(lines, "- "+p)
		}
	}
	slices.SortFunc(lines, func(x, y string) int { return strings.Compare(x[2:], y[2:]) })
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}

func sums(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out, err
}
