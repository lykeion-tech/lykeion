package report

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"lykeion/server/internal/handin"
)

// Span is a phrase of the study's hand-in, by byte range, that links to a node.
type Span struct {
	Start, End int
	Node       string
	Phrase     string
}

// Proposals are the parts of a report run's hand-in that rest on the study's
// own text. They are still only proposals until Jev accepts them.
type Proposals struct {
	Concepts []handin.ReportConcept
	Facts    []handin.ReportFact
	Links    []Span
}

func normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// find is the part of src that q matches, ignoring case and runs of
// whitespace, as src spells it.
func find(src, q string) (string, bool) {
	nq := normalize(q)
	if nq == "" {
		return "", false
	}
	// norm is src normalized; from and to are, for each of its bytes, the
	// bytes of src it came from.
	var norm []byte
	var from, to []int
	gap := false
	for i, r := range src {
		if unicode.IsSpace(r) {
			gap = len(norm) > 0
			continue
		}
		if gap {
			norm, from, to, gap = append(norm, ' '), append(from, i), append(to, i), false
		}
		l := string(unicode.ToLower(r))
		_, w := utf8.DecodeRuneInString(src[i:])
		for range len(l) {
			from, to = append(from, i), append(to, i+w)
		}
		norm = append(norm, l...)
	}
	k := strings.Index(string(norm), nq)
	if k < 0 {
		return "", false
	}
	return src[from[k]:to[k+len(nq)-1]], true
}

// Ground keeps a proposal only when its quote or phrase is in the study's
// own recorded text and everything it names exists. A kept fact's quote is
// that text as the study spells it, not as the agent wrote it.
func Ground(rep Report, h handin.ReportHandIn) Proposals {
	var p Proposals
	nodes := map[string]Node{}
	for _, n := range rep.Nodes {
		nodes[n.ID] = n
	}
	keys := map[string]bool{}
	for _, c := range h.Concepts {
		keys[c.Key] = true
	}
	used := map[string]bool{}
	for _, f := range h.Facts {
		n, ok := nodes[f.SourceNode]
		if !ok || !keys[f.Subject] {
			continue
		}
		q, found := find(n.Quote, f.Quote)
		if !found {
			q, found = find(n.Label, f.Quote)
		}
		if !found {
			continue
		}
		f.Quote = q
		if f.Relation == "mentioned_in" {
			if _, ok := nodes[f.Object]; !ok {
				continue
			}
		} else if !keys[f.Object] {
			continue
		}
		p.Facts = append(p.Facts, f)
		used[f.Subject] = true
		if f.Relation != "mentioned_in" {
			used[f.Object] = true
		}
	}
	for _, c := range h.Concepts {
		if used[c.Key] {
			p.Concepts = append(p.Concepts, c)
		}
	}
	if rep.HandIn == nil {
		return p
	}
	text := rep.HandIn.Text
	for _, l := range h.Links {
		if n, ok := nodes[l.Node]; !ok || !Linkable(n.Kind) || l.Phrase == "" || l.Occurrence < 1 {
			continue
		}
		at, from := -1, 0
		for i := 0; i < l.Occurrence; i++ {
			j := strings.Index(text[from:], l.Phrase)
			if j < 0 {
				at = -1
				break
			}
			at = from + j
			from = at + 1
		}
		if at >= 0 && plainProse(text, at, at+len(l.Phrase)) {
			p.Links = append(p.Links, Span{Start: at, End: at + len(l.Phrase), Node: l.Node, Phrase: l.Phrase})
		}
	}
	sort.SliceStable(p.Links, func(i, j int) bool { return p.Links[i].Start < p.Links[j].Start })
	kept, end := p.Links[:0:0], 0
	for _, s := range p.Links {
		if s.Start >= end {
			kept = append(kept, s)
			end = s.End
		}
	}
	p.Links = kept
	return p
}

// plainProse says whether the hand-in's Markdown shows text[start:end] as one
// run of ordinary text, which is the only place the report page can make a
// phrase clickable. A phrase that carries Markdown marks, or sits in a code
// span, a fenced block, a link's text or a URL, is not, so Jev is never asked
// about it.
func plainProse(text string, start, end int) bool {
	if strings.ContainsAny(text[start:end], "*_`[]<>") {
		return false
	}
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	line := text[lineStart:start]
	if strings.Count(line, "`")%2 == 1 {
		return false
	}
	if open := strings.LastIndexByte(line, '['); open > strings.LastIndexByte(line, ']') {
		return false
	}
	fences := 0
	for _, l := range strings.Split(text[:lineStart], "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fences++
		}
	}
	if fences%2 == 1 {
		return false
	}
	from := strings.LastIndexAny(text[:start], " \t\n") + 1
	to := end + strings.IndexAny(text[end:]+" ", " \t\n")
	token := text[from:to]
	return !strings.Contains(token, "://") && !strings.HasPrefix(token, "www.") && !strings.Contains(token, "@")
}

// Linkable says whether a hand-in link may point at a node of kind: only the
// study's Grounding sources, its papers and datasets, may be linked.
func Linkable(kind string) bool { return kind == "paper" || kind == "dataset" }
