package handin

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
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

// pmidField is a "pmid" field of a sources hit in a call's answer. Its number
// is indexed as pmid:N only there, so a bare number elsewhere in an answer
// never counts as a PubMed id.
var pmidField = regexp.MustCompile(`"pmid"\s*:\s*"?(\d{1,9})\b`)

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
	saved     map[string]bool     // names of the study's output files
	scripts   map[string][]string // a script's path → its run_script runs, run:KEY
	attached  map[string]bool     // names of the study's attached files
	hyps      map[string]bool     // the Hypotheses column's hypothesis texts, trimmed
	values    map[string]bool     // names of the Experimentation column's values
	// Mode is how Validation checks the study, set by the caller: "runs" when
	// Experimentation's lead handed in, else "claims".
	Mode string
}

// ModeOf is how Validation checks a study, from its records in every
// column: "runs" when Experimentation's lead handed in, else "claims".
func ModeOf(study []db.Record) string {
	for _, r := range study {
		if r.BoardColumn == board.Experimentation && r.Kind == "summary" && Leads(r.Role) && r.SupersededAt == nil {
			return "runs"
		}
	}
	return "claims"
}

// NewEvidence indexes the live records among recs, from every column.
// A call's answer is searched as stored, which is cut at records.MaxCallAnswer.
func NewEvidence(recs []db.Record) Evidence {
	e := Evidence{found: map[string][]string{}, grounding: map[string]string{}, saved: map[string]bool{}, scripts: map[string][]string{},
		attached: map[string]bool{}, hyps: map[string]bool{}, values: map[string]bool{}}
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
			Name      string   `json:"name"`
			Tool      string   `json:"tool"`
			Path      string   `json:"path"`
			Server    string   `json:"server"`
			Text      string   `json:"text"`
			Args      struct {
				Name string `json:"name"`
			} `json:"args"`
		}
		_ = json.Unmarshal(r.Body, &b)
		switch r.Kind {
		case "call":
			ref := "call:" + r.Key
			for _, t := range tokens(b.Answer) {
				e.add(t, ref)
			}
			for _, m := range pmidField.FindAllStringSubmatch(b.Answer, -1) {
				e.add("pmid:"+m[1], ref)
			}
			if b.Server == records.AttachmentServer && b.Args.Name != "" {
				e.attached[b.Args.Name] = true
			}
		case review.KindHypothesis:
			if r.BoardColumn == board.Hypotheses {
				e.hyps[strings.TrimSpace(b.Text)] = true
			}
		case "value":
			if r.BoardColumn == board.Experimentation {
				e.values[b.Name] = true
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
		case "output":
			e.saved[b.Name] = true
		case "run":
			if b.Tool == "run_script" {
				e.scripts[b.Path] = append(e.scripts[b.Path], "run:"+r.Key)
			}
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

// Saved says whether an output file named name was saved in the study, by
// any of its runs.
func (e Evidence) Saved(name string) bool { return e.saved[name] }

// Attached says whether a file named name is attached to the study.
func (e Evidence) Attached(name string) bool { return e.attached[name] }

// Hypothesis says whether text, trimmed, is a hypothesis of the study.
func (e Evidence) Hypothesis(text string) bool { return e.hyps[strings.TrimSpace(text)] }

// Value says whether the study recorded a value named name.
func (e Evidence) Value(name string) bool { return e.values[name] }

// Scripts are the recorded run_script runs of the script at path, written
// run:KEY as a value cites them; nil when none was recorded.
func (e Evidence) Scripts(path string) []string { return e.scripts[path] }
