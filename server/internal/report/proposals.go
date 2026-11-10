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
		if _, ok := nodes[l.Node]; !ok || l.Phrase == "" || l.Occurrence < 1 {
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
		if at >= 0 {
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
