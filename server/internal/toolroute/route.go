// Package toolroute lets Jev route an agent's plain-language need to the
// skills and connector tools it may use. Jev judges relevance level by level
// (domain, then each skill or connector, then a skill's sections or a
// connector's tools), keeps every item at or above Threshold and is never
// made to pick a top item.
package toolroute

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"lykeion/server/internal/connectors"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/science"
)

// Threshold is the Jev probability at which an item qualifies.
const Threshold = 0.5

// maxQuestions keeps one Jev request under its token limit.
const maxQuestions = 200

// excerptMax bounds the text one question quotes from a description or section.
const excerptMax = 600

// ErrNoJev is routing asked of a lab with no TypeSafe key.
var ErrNoJev = errors.New("Jev is unavailable: the lab has no TypeSafe key")

type Skill struct {
	Name, Domain, Description string
	// Sections are read on demand when nil: from Files, or from the bundle.
	Sections []science.Section
	// Files is an archive skill's whole folder; nil for a bundled skill.
	Files map[string][]byte
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Connector is named as call_tool names it.
type Connector struct {
	Name, Domain, Description string
	Tools                     []Tool
}

type Candidates struct {
	Skills     []Skill
	Connectors []Connector
}

type RoutedSkill struct {
	Name     string            `json:"name"`
	Sections []science.Section `json:"sections"`
	// Folder is the skill's whole folder as a tar stream, for the daemon to
	// write into the CLI's skills folder.
	Folder []byte `json:"folder,omitempty"`
}

type RoutedTool struct {
	Server      string `json:"server"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
}

type Result struct {
	Skills []RoutedSkill `json:"skills"`
	Tools  []RoutedTool  `json:"tools"`
}

// Empty says nothing qualified.
func (r Result) Empty() bool { return len(r.Skills) == 0 && len(r.Tools) == 0 }

// teamGroup is the domain a Team's own skills and servers are judged under.
func teamGroup(m pack.Manifest) string {
	if m.Domain != "" {
		return m.Domain
	}
	return "the " + m.Name + " Team's own tools"
}

// ForTeam is what a Team member's run routes to: the member's declared
// skills and the connectors it may call. tools are the door's tools/list
// answers, by connector name.
func ForTeam(p *pack.Pack, m pack.Member, conns []pack.Connector, tools map[string][]Tool) Candidates {
	group := teamGroup(p.Manifest)
	var c Candidates
	for _, name := range m.Skills {
		if files := p.SkillFiles(m.Name, name); files["SKILL.md"] != nil {
			desc, secs := science.ReadSkill(files["SKILL.md"])
			if desc == "" && len(secs) > 0 {
				desc = secs[0].Text
			}
			c.Skills = append(c.Skills, Skill{Name: name, Domain: group, Description: desc, Sections: secs, Files: files})
			continue
		}
		if i := slices.IndexFunc(science.Skills(), func(s science.Skill) bool { return s.Name == name }); i >= 0 {
			s := science.Skills()[i]
			c.Skills = append(c.Skills, Skill{Name: s.Name, Domain: s.Domain, Description: s.Description})
		}
	}
	c.Connectors = connectorsOf(conns, tools, group)
	return c
}

// ForLibrary is what a run without a Team routes to: every bundled skill and
// the given connectors.
func ForLibrary(conns []pack.Connector, tools map[string][]Tool) Candidates {
	var c Candidates
	for _, s := range science.Skills() {
		c.Skills = append(c.Skills, Skill{Name: s.Name, Domain: s.Domain, Description: s.Description})
	}
	c.Connectors = connectorsOf(conns, tools, "the workspace's own servers")
	return c
}

// connectorsOf names each connector as call_tool does. A catalogue connector
// has the one tool fetch, described by its catalogue entry; any other is
// judged by the tools its server lists, and one that lists none is left out.
func connectorsOf(conns []pack.Connector, tools map[string][]Tool, group string) []Connector {
	var out []Connector
	for _, pc := range conns {
		name := pc.Name
		if name == "" {
			name = pc.Catalogue
		}
		if pc.Catalogue != "" {
			e, ok := connectors.Get(pc.Catalogue)
			if !ok {
				continue
			}
			out = append(out, Connector{Name: name, Domain: e.Domain, Description: e.Purpose,
				Tools: []Tool{{Name: "fetch", Description: "Fetch from " + e.Name + ": " + e.Purpose}}})
			continue
		}
		ts := tools[name]
		if len(ts) == 0 {
			continue
		}
		var listed []string
		for _, t := range ts {
			listed = append(listed, t.Name)
		}
		out = append(out, Connector{Name: name, Domain: group, Description: "an MCP server with the tools " + strings.Join(listed, ", "), Tools: ts})
	}
	return out
}

// Route walks the three levels for need. Each level is one batch of Jev
// yes/no questions, split into requests of at most maxQuestions.
func Route(ctx context.Context, j *jev.Client, need string, c Candidates) (Result, error) {
	res := Result{Skills: []RoutedSkill{}, Tools: []RoutedTool{}}
	if j == nil {
		return res, ErrNoJev
	}
	state := fence.Untrusted("What an agent working on a researcher's study needs", need)

	domains := map[string]bool{}
	var order []string
	for _, s := range c.Skills {
		if !domains[s.Domain] {
			domains[s.Domain] = true
			order = append(order, s.Domain)
		}
	}
	for _, k := range c.Connectors {
		if !domains[k.Domain] {
			domains[k.Domain] = true
			order = append(order, k.Domain)
		}
	}
	var qs []string
	for _, d := range order {
		qs = append(qs, "Is this field relevant to the need?\n"+fence.Untrusted("The field", d))
	}
	keep, err := ask(ctx, j, state, qs)
	if err != nil {
		return res, err
	}
	for i, d := range order {
		domains[d] = keep[i]
	}

	var skills []Skill
	var conns []Connector
	qs = nil
	for _, s := range c.Skills {
		if domains[s.Domain] {
			skills = append(skills, s)
			qs = append(qs, fmt.Sprintf("Could the skill %q help with this need?\n", s.Name)+fence.Untrusted("What the skill says of itself", excerpt(s.Description)))
		}
	}
	for _, k := range c.Connectors {
		if domains[k.Domain] {
			conns = append(conns, k)
			qs = append(qs, fmt.Sprintf("Could the connector %q help with this need?\n", k.Name)+fence.Untrusted("What the connector offers", excerpt(k.Description)))
		}
	}
	if keep, err = ask(ctx, j, state, qs); err != nil {
		return res, err
	}
	skills, conns = kept(skills, keep[:len(skills)]), kept(conns, keep[len(skills):])

	type sectionOf struct {
		skill int
		sec   science.Section
	}
	type toolOf struct {
		server string
		tool   Tool
	}
	var secs []sectionOf
	var tools []toolOf
	qs = nil
	for i, s := range skills {
		all, err := s.sections()
		if err != nil {
			return res, fmt.Errorf("skill %s: %w", s.Name, err)
		}
		for _, sec := range all {
			secs = append(secs, sectionOf{i, sec})
			qs = append(qs, fmt.Sprintf("Does this section of the skill %q help with this need?\n", s.Name)+fence.Untrusted("The section", "## "+sec.Heading+"\n"+excerpt(sec.Text)))
		}
	}
	for _, k := range conns {
		for _, t := range k.Tools {
			tools = append(tools, toolOf{k.Name, t})
			qs = append(qs, fmt.Sprintf("Does the tool %q of the connector %q help with this need?\n", t.Name, k.Name)+fence.Untrusted("What the tool does", excerpt(t.Description)))
		}
	}
	if keep, err = ask(ctx, j, state, qs); err != nil {
		return res, err
	}
	routed := map[int]int{} // skill index → index in res.Skills
	for i, s := range secs {
		if !keep[i] {
			continue
		}
		at, ok := routed[s.skill]
		if !ok {
			at = len(res.Skills)
			routed[s.skill] = at
			res.Skills = append(res.Skills, RoutedSkill{Name: skills[s.skill].Name})
		}
		res.Skills[at].Sections = append(res.Skills[at].Sections, s.sec)
	}
	for i, t := range tools {
		if keep[len(secs)+i] {
			res.Tools = append(res.Tools, RoutedTool{Server: t.server, Tool: t.tool.Name, Description: t.tool.Description})
		}
	}
	return res, nil
}

// AttachFolders gives each routed skill its whole folder as a tar stream.
func (c Candidates) AttachFolders(r *Result) error {
	for i := range r.Skills {
		at := slices.IndexFunc(c.Skills, func(s Skill) bool { return s.Name == r.Skills[i].Name })
		if at < 0 {
			return fmt.Errorf("routed skill %s is not a candidate", r.Skills[i].Name)
		}
		var buf bytes.Buffer
		if err := c.Skills[at].tar(&buf); err != nil {
			return fmt.Errorf("skill %s: %w", r.Skills[i].Name, err)
		}
		r.Skills[i].Folder = buf.Bytes()
	}
	return nil
}

func (s Skill) sections() ([]science.Section, error) {
	switch {
	case s.Sections != nil:
		return s.Sections, nil
	case s.Files != nil:
		_, secs := science.ReadSkill(s.Files["SKILL.md"])
		return secs, nil
	}
	return science.Sections(s.Name)
}

func (s Skill) tar(w io.Writer) error {
	if s.Files == nil {
		return science.Tar(w, s.Name)
	}
	tw := tar.NewWriter(w)
	for _, p := range slices.Sorted(maps.Keys(s.Files)) {
		b := s.Files[p]
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: p, Mode: 0o644, Size: int64(len(b))}); err != nil {
			return err
		}
		if _, err := tw.Write(b); err != nil {
			return err
		}
	}
	return tw.Close()
}

// ask answers whether each question qualifies.
func ask(ctx context.Context, j *jev.Client, state string, qs []string) ([]bool, error) {
	keep := make([]bool, len(qs))
	for start := 0; start < len(qs); start += maxQuestions {
		end := min(start+maxQuestions, len(qs))
		batch := map[string]string{}
		for i := start; i < end; i++ {
			batch[fmt.Sprintf("q%d", i)] = qs[i]
		}
		ps, err := j.Noul(ctx, state, batch)
		if err != nil {
			return nil, fmt.Errorf("Jev is unavailable: %w", err)
		}
		for i := start; i < end; i++ {
			keep[i] = ps[fmt.Sprintf("q%d", i)] >= Threshold
		}
	}
	return keep, nil
}

func kept[T any](items []T, keep []bool) []T {
	var out []T
	for i, it := range items {
		if keep[i] {
			out = append(out, it)
		}
	}
	return out
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= excerptMax {
		return s
	}
	return strings.ToValidUTF8(s[:excerptMax], "") + " …"
}
