package handin

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

// NormID is an id in the one spelling the index compares: lower case, a
// doi.org URL reduced to its DOI, a bare DOI written doi:10.…; arXiv ids keep
// their arxiv: prefix.
func NormID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/"} {
		if rest, ok := strings.CutPrefix(s, p); ok {
			rest, _, _ = strings.Cut(rest, "?")
			rest, _, _ = strings.Cut(rest, "#")
			return "doi:" + rest
		}
	}
	for _, p := range []string{"https://arxiv.org/abs/", "http://arxiv.org/abs/", "https://arxiv.org/pdf/", "http://arxiv.org/pdf/"} {
		if rest, ok := strings.CutPrefix(s, p); ok {
			rest, _, _ = strings.Cut(rest, "?")
			rest, _, _ = strings.Cut(rest, "#")
			s = "arxiv:" + strings.TrimSuffix(rest, ".pdf")
			break
		}
	}
	if strings.HasPrefix(s, "arxiv:") {
		s = arxivVersion.ReplaceAllString(s, "")
	}
	if strings.HasPrefix(s, "10.") {
		return "doi:" + s
	}
	return s
}

// arxivVersion is the vN a hand-in may add to an arXiv id.
var arxivVersion = regexp.MustCompile(`v[0-9]+$`)

// forms are the spellings one id answers to: its own, and for an arXiv id
// the arXiv DOI, so a hand-in naming either finds a record holding the other.
func forms(id string) []string {
	n := NormID(id)
	if ref, ok := strings.CutPrefix(n, "arxiv:"); ok {
		return []string{n, "doi:10.48550/arxiv." + ref}
	}
	if ref, ok := strings.CutPrefix(n, "doi:10.48550/arxiv."); ok {
		return []string{n, "arxiv:" + ref}
	}
	return []string{n}
}

// Evidence is what a study's sources server recorded: which ids came back,
// and from which records. Only those records count; an agent's own text is
// never read.
type Evidence struct {
	found     map[string][]string
	grounding map[string]string
}

// NewEvidence indexes the live records among recs, from every column.
// A call's answer is searched as stored, which is cut at records.MaxCallAnswer.
func NewEvidence(recs []db.Record) Evidence {
	e := Evidence{found: map[string][]string{}, grounding: map[string]string{}}
	for _, r := range recs {
		if r.SupersededAt != nil {
			continue
		}
		if review.GroundingSource(r) {
			if id := review.IdentityOf(r); id != "" {
				for _, f := range forms(id) {
					if _, ok := e.grounding[f]; !ok {
						e.grounding[f] = r.Key
					}
				}
			}
		}
		var b struct {
			Answer    string   `json:"answer"`
			Results   []string `json:"results"`
			SourceID  string   `json:"source_id"`
			DOI       string   `json:"doi"`
			Accession string   `json:"accession"`
		}
		_ = json.Unmarshal(r.Body, &b)
		switch r.Kind {
		case "call":
			ref := "call:" + r.Key
			for _, t := range tokens(b.Answer) {
				e.add(t, ref)
			}
		case "search":
			for _, id := range b.Results {
				e.add(id, "search:"+r.Key)
			}
		case "fetch":
			e.add(b.SourceID, "fetch:"+r.Key)
			e.add(b.DOI, "fetch:"+r.Key)
		case "open_dataset":
			e.add(b.Accession, "open_dataset:"+r.Key)
		}
	}
	return e
}

func (e Evidence) add(id, ref string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	for _, f := range forms(id) {
		if !slices.Contains(e.found[f], ref) {
			e.found[f] = append(e.found[f], ref)
		}
	}
}

// tokens are the whole words of free text. A word ends at whitespace, quotes,
// angle brackets, commas, square or curly brackets; trailing punctuation
// (. ; : ) ? ! is also tried off its end, so a DOI closing a sentence is found
// while 10.1000/abc is never found inside 10.1000/abcd.
func tokens(s string) []string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n\"'`<>,[]{}", r)
	})
	out := make([]string, 0, 2*len(words))
	for _, w := range words {
		out = append(out, w)
		if t := strings.TrimLeft(strings.TrimRight(w, ".;:)?!"), "("); t != w && t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Sources are the records that returned id. A call record is written
// call:KEY, the form a cite takes; the sources server's own search, fetch and
// open_dataset records are written kind:KEY and are not citable. Nil when no
// record returned it.
func (e Evidence) Sources(id string) []string {
	var out []string
	for _, f := range forms(id) {
		for _, ref := range e.found[f] {
			if !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
	}
	return out
}

// Grounding is the key of a Grounding paper or dataset record with id's
// identity.
func (e Evidence) Grounding(id string) (key string, ok bool) {
	for _, f := range forms(id) {
		if key, ok = e.grounding[f]; ok {
			return key, true
		}
	}
	return "", false
}
