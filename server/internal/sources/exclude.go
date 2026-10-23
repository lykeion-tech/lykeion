package sources

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// Exclusions are sources the server keeps from agents: papers a study must
// not read, such as the one whose result it is asked to predict. Each line of
// the file is a DOI, an arXiv id, a PubMed Central id or a title; blank lines
// and # comments are skipped. A title shorter than minTitle could name other
// papers, so it is ignored.
type Exclusions struct {
	ids    []*regexp.Regexp // DOIs, arXiv and PMC ids, whole
	titles []string
}

const (
	minTitle = 30
	// A published title may add words to the one listed, so a result's title
	// that holds every listed word plus at most extraWords others is the same
	// paper.
	extraWords = 5
)

var (
	arxivID = regexp.MustCompile(`^\d{4}\.\d{4,5}$`)
	pmcID   = regexp.MustCompile(`^(?i)pmc\d+$`)
	notWord = regexp.MustCompile(`[^a-z0-9]+`)
)

func LoadExclusions(path string) (*Exclusions, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	x := &Exclusions{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "10."):
			x.ids = append(x.ids, regexp.MustCompile(regexp.QuoteMeta(strings.ToLower(line))+`($|[^a-z0-9])`))
		case pmcID.MatchString(line):
			x.ids = append(x.ids, regexp.MustCompile(`(^|[^a-z0-9])`+strings.ToLower(line)+`($|[^\d])`))
		case arxivID.MatchString(line):
			x.ids = append(x.ids, regexp.MustCompile(`(^|[^\d])`+regexp.QuoteMeta(line)+`($|[^\d])`))
		default:
			if t := normTitle(line); len(t) >= minTitle {
				x.titles = append(x.titles, t)
			}
		}
	}
	return x, sc.Err()
}

// normTitle lowercases s and keeps its words, so spelling, hyphens and
// punctuation don't hide a match.
func normTitle(s string) string {
	return " " + strings.Join(strings.Fields(notWord.ReplaceAllString(strings.ToLower(s), " ")), " ") + " "
}

// Match reports whether s names an excluded source. A nil Exclusions keeps
// nothing back.
func (x *Exclusions) Match(s string) bool {
	if x == nil {
		return false
	}
	low := strings.ToLower(s)
	for _, re := range x.ids {
		if re.MatchString(low) {
			return true
		}
	}
	if len(x.titles) > 0 {
		norm := normTitle(s)
		for _, t := range x.titles {
			if strings.Contains(norm, t) {
				return true
			}
		}
	}
	return false
}

// Hides reports whether a search result or fetched paper is an excluded
// source: by its ids, or by a title that is a listed one, perhaps with a few
// words added.
func (x *Exclusions) Hides(hit Hit) bool {
	if x == nil {
		return false
	}
	if x.Match(hit.Title + "\n" + hit.DOI + "\n" + hit.ID) {
		return true
	}
	words := map[string]bool{}
	for _, w := range strings.Fields(normTitle(hit.Title)) {
		words[w] = true
	}
	for _, t := range x.titles {
		listed := strings.Fields(t)
		all := true
		for _, w := range listed {
			all = all && words[w]
		}
		if all && len(words) <= len(listed)+extraWords {
			return true
		}
	}
	return false
}
