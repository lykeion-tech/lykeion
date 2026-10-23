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

// maxQuestions keeps one Jev request under its 64k-token limit. A question
// is at most about 1.1 KB: two names of nameMax, an excerpt of excerptMax and
// its fence. 100 of them with the fenced need come to about 112 KB, some 28k
// tokens at 4 bytes a token, which leaves room for JSON escapes and text that
// tokenizes worse than English.
const maxQuestions = 100

// nameMax bounds a name a question quotes: a skill, connector, tool or field.
const nameMax = 128

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
	// LoadFiles reads an immutable imported folder after item qualification.
	LoadFiles func(context.Context) (map[string][]byte, error) `json:"-"`
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
	// Own is the domain of a Team's own skills and connectors. It is the
	// Team's own field, so Jev judges its items but is not asked whether it
	// is relevant.
	Own string
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
	// Judged is every item Jev was asked about, with its probability, so the
	// threshold can be tuned from recorded evidence. It stays on the server.
	Judged []Judgement `json:"-"`
}

// Judgement is Jev's probability for one item at one level of the walk.
type Judgement struct {
	Level string  `json:"level"`
	Name  string  `json:"name"`
	Noul  float64 `json:"noul"`
	Kept  bool    `json:"kept"`
}

// Empty says nothing qualified.
func (r Result) Empty() bool { return len(r.Skills) == 0 && len(r.Tools) == 0 }

// teamGroup is the domain a Team's own skills and servers are judged under.
func teamGroup(m pack.Manifest) string {
	if m.Domain != "" {
		return m.Domain
	}
	return "the " + strings.TrimSuffix(m.Name, " Team") + " Team's own tools"
}

// ForTeam is what a Team member's run routes to: the member's declared
// skills and the connectors it may call. tools are the door's tools/list
// answers, by connector name.
func ForTeam(p *pack.Pack, m pack.Member, conns []pack.Connector, tools map[string][]Tool) Candidates {
	group := teamGroup(p.Manifest)
	c := Candidates{Own: group}
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

// ForLibrary routes a run without a Team to its supplied skill library and connectors.
func ForLibrary(conns []pack.Connector, tools map[string][]Tool, skills []Skill) Candidates {
	return Candidates{Skills: skills, Connectors: connectorsOf(conns, tools, "the workspace's own servers")}
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
	order = slices.DeleteFunc(order, func(d string) bool { return c.Own != "" && d == c.Own })
	var qs []string
	for _, d := range order {
		qs = append(qs, "Is this field relevant to the need?\n"+fence.Untrusted("The field", cut(d, nameMax)))
	}
	ps, err := ask(ctx, j, state, qs)
	if err != nil {
		return res, err
	}
	keep := res.judge("field", order, ps)
	for i, d := range order {
		domains[d] = keep[i]
	}

	var skills []Skill
	var conns []Connector
	qs = nil
	var names []string
	for _, s := range c.Skills {
		if domains[s.Domain] {
			skills = append(skills, s)
			names = append(names, s.Name)
			qs = append(qs, fmt.Sprintf("Could the skill %q help with this need?\n", cut(s.Name, nameMax))+fence.Untrusted("What the skill says of itself", excerpt(s.Description)))
		}
	}
	for _, k := range c.Connectors {
		if domains[k.Domain] {
			conns = append(conns, k)
			names = append(names, k.Name)
			qs = append(qs, fmt.Sprintf("Could the connector %q help with this need?\n", cut(k.Name, nameMax))+fence.Untrusted("What the connector offers", excerpt(k.Description)))
		}
	}
	if ps, err = ask(ctx, j, state, qs); err != nil {
		return res, err
	}
	keep = res.judge("item", names, ps)
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
	qs, names = nil, nil
	for i, s := range skills {
		all, err := s.sections(ctx)
		if err != nil {
			return res, fmt.Errorf("skill %s: %w", s.Name, err)
		}
		for _, sec := range all {
			secs = append(secs, sectionOf{i, sec})
			names = append(names, s.Name+" / "+sec.Heading)
			qs = append(qs, fmt.Sprintf("Does this section of the skill %q help with this need?\n", cut(s.Name, nameMax))+fence.Untrusted("The section", excerpt("## "+sec.Heading+"\n"+sec.Text)))
		}
	}
	for _, k := range conns {
		for _, t := range k.Tools {
			tools = append(tools, toolOf{k.Name, t})
			names = append(names, k.Name+" / "+t.Name)
			qs = append(qs, fmt.Sprintf("Does the tool %q of the connector %q help with this need?\n", cut(t.Name, nameMax), cut(k.Name, nameMax))+fence.Untrusted("What the tool does", excerpt(t.Description)))
		}
	}
	if ps, err = ask(ctx, j, state, qs); err != nil {
		return res, err
	}
	keep = res.judge("part", names, ps)
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

func (s Skill) sections(ctx context.Context) ([]science.Section, error) {
	if s.Files == nil && s.LoadFiles != nil {
		files, err := s.LoadFiles(ctx)
		if err != nil {
			return nil, err
		}
		s.Files = files
	}
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

// ask returns Jev's probability for each question.
func ask(ctx context.Context, j *jev.Client, state string, qs []string) ([]float64, error) {
	keep := make([]float64, len(qs))
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
			keep[i] = ps[fmt.Sprintf("q%d", i)]
		}
	}
	return keep, nil
}

// judge records each probability under level and names, and returns which
// items qualify.
func (r *Result) judge(level string, names []string, ps []float64) []bool {
	keep := make([]bool, len(ps))
	for i, p := range ps {
		keep[i] = p >= Threshold
		r.Judged = append(r.Judged, Judgement{Level: level, Name: names[i], Noul: p, Kept: keep[i]})
	}
	return keep
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

func excerpt(s string) string { return cut(strings.TrimSpace(s), excerptMax) }

// cut keeps at most n bytes of s, at a UTF-8 boundary, and marks a cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + " …"
}
