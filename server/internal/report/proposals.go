package report

import (
	"regexp"
	"slices"
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
	mask := notPlain(text)
	for _, l := range h.Links {
		if n, ok := nodes[l.Node]; !ok || !Linkable(n.Kind) || l.Phrase == "" || l.Occurrence < 1 {
			continue
		}
		// The page finds a phrase by counting its copies, so one copy it
		// can't link means it links none of them.
		var copies []int
		plain := true
		for at := strings.Index(text, l.Phrase); at >= 0 && plain; {
			copies = append(copies, at)
			plain = !slices.Contains(mask[at:at+len(l.Phrase)], true)
			next := strings.Index(text[at+len(l.Phrase):], l.Phrase)
			if next < 0 {
				break
			}
			at += len(l.Phrase) + next
		}
		if plain && l.Occurrence <= len(copies) {
			at := copies[l.Occurrence-1]
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

// Patterns of text the page's Markdown renderer turns into links of its own:
// DOIs as its doi_links rule finds them, URLs, emails, and bare domains.
var autoLinks = regexp.MustCompile(`(?i)\b(?:doi:\s*)?10\.\d{4,9}/[-._;()/:a-z0-9]+` +
	`|(?:\b[a-z][a-z0-9+.-]*:)?//\S+|\bwww\.\S+|\bmailto:\S+` +
	`|[a-z0-9._%+-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+` +
	`|\b(?:[a-z0-9-]+\.)+(?:biz|com|edu|gov|net|org|pro|web|xxx|aero|asia|coop|info|museum|name|shop|[a-z]{2})\b(?:[/?#]\S*)?`)

// Markdown links and images, with their text and destination, and the lines
// that define a reference link.
var mdLinks = regexp.MustCompile(`!?\[[^\]]*\](?:\([^)]*\)|\[[^\]]*\])?|(?m)^ {0,3}\[[^\]]+\]:.*$`)

// notPlain marks each byte of the hand-in's Markdown that the report page
// can't make part of a clickable phrase: Markdown marks, line breaks, code
// blocks and spans, link text and destinations, and whatever the page turns
// into a link itself.
//
// ponytail: line rules and regexes shaped like markdown-it, not a parser. List
// items' code, HTML entities and setext headings aren't modelled, and in
// doubt a byte is marked; port the renderer's parser if that drops too much.
func notPlain(text string) []bool {
	mask := make([]bool, len(text))
	set := func(a, b int) {
		for i := a; i < b; i++ {
			mask[i] = true
		}
	}
	// Block pass: fenced and indented code are marked whole; the paragraphs
	// and headings left are where a code span can open and close.
	var blocks [][2]int
	fence, para, at := "", -1, 0
	flush := func(end int) {
		if para >= 0 {
			blocks, para = append(blocks, [2]int{para, end}), -1
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		start, end := at, at+len(line)
		at = end
		body := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimLeft(body, " \t")
		col := 0
		for _, c := range body[:len(body)-len(trimmed)] {
			if c == '\t' {
				col += 4 - col%4
			} else {
				col++
			}
		}
		if fence != "" {
			set(start, end)
			run := len(trimmed) - len(strings.TrimLeft(trimmed, fence[:1]))
			if col < 4 && run >= len(fence) && strings.TrimSpace(trimmed[run:]) == "" {
				fence = ""
			}
			continue
		}
		if col < 4 && trimmed != "" && (trimmed[0] == '`' || trimmed[0] == '~') {
			run := len(trimmed) - len(strings.TrimLeft(trimmed, trimmed[:1]))
			if run >= 3 && !(trimmed[0] == '`' && strings.Contains(trimmed[run:], "`")) {
				flush(start)
				set(start, end)
				fence = trimmed[:run]
				continue
			}
		}
		switch {
		case trimmed == "":
			flush(start)
		case col >= 4 && para < 0:
			set(start, end)
		case strings.HasPrefix(trimmed, "#"):
			flush(start)
			blocks = append(blocks, [2]int{start, end})
		case para < 0:
			para = start
		}
	}
	flush(len(text))
	// A run of n backticks that isn't escaped opens a code span closed by
	// the next run of exactly n in the same block.
	for _, b := range blocks {
		s := text[b[0]:b[1]]
		for i := 0; i < len(s); {
			if s[i] != '`' {
				i++
				continue
			}
			n := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
			if slashes := len(s[:i]) - len(strings.TrimRight(s[:i], "\\")); slashes%2 == 1 {
				i++
				continue
			}
			end := -1
			for j := i + n; j < len(s); {
				k := strings.IndexByte(s[j:], '`')
				if k < 0 {
					break
				}
				j += k
				m := len(s[j:]) - len(strings.TrimLeft(s[j:], "`"))
				if m == n {
					end = j + m
					break
				}
				j += m
			}
			if end < 0 {
				i += n
				continue
			}
			set(b[0]+i, b[0]+end)
			i = end
		}
	}
	for _, re := range []*regexp.Regexp{mdLinks, autoLinks} {
		for _, m := range re.FindAllStringIndex(text, -1) {
			set(m[0], m[1])
		}
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '*', '`', '[', ']', '\\', '\n', '\r':
			mask[i] = true
		case '~':
			mask[i] = i+1 < len(text) && text[i+1] == '~' || i > 0 && text[i-1] == '~'
		case '_':
			// An underscore run between two letters or digits is literal.
			j := i + len(text[i:]) - len(strings.TrimLeft(text[i:], "_"))
			before, _ := utf8.DecodeLastRuneInString(text[:i])
			after, _ := utf8.DecodeRuneInString(text[j:])
			if !word(before) || !word(after) {
				set(i, j)
			}
			i = j - 1
		}
	}
	return mask
}

// Linkable says whether a hand-in link may point at a node of kind: only the
// study's Grounding sources, its papers and datasets, may be linked.
func Linkable(kind string) bool { return kind == "paper" || kind == "dataset" }
