package report

import (
	"context"
	"fmt"
	"strings"

	"lykeion/server/internal/fence"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/review"
)

// MaxJevQuestions is the most questions one report asks Jev; proposals past
// it are not asked and not kept.
const MaxJevQuestions = 200

const (
	factQuestion    = "Does the evidence state that the item holds? Answer yes only if the quoted evidence says so."
	linkQuestion    = "Does the phrase refer to this item?"
	figureQuestion  = "Does this figure, as its recorded text describes it, show what the claim says?"
	tableQuestion   = "Does this table's data match the claim?"
	noveltyQuestion = "Reading only the paper's abstract, does it confirm the finding, contradict it, or is it unrelated to it?"
)

var noveltyOptions = map[string]string{
	"confirms":    "The abstract reports the same result as the finding.",
	"contradicts": "The abstract reports a result that contradicts the finding.",
	"unrelated":   "The abstract does not address the finding's result.",
}

type JudgedFact struct {
	handin.ReportFact
	P float64
}

type JudgedSpan struct {
	Span
	P float64
}

// Judged is what Jev accepted of a report run's proposals.
type Judged struct {
	Concepts []handin.ReportConcept
	Facts    []JudgedFact
	Links    []JudgedSpan
}

// Judge asks Jev about each fact, then each link, against its quoted
// evidence, up to MaxJevQuestions, and keeps those it accepts and the
// concepts a kept fact still mentions, each once. Identical facts are
// asked and kept once.
func Judge(ctx context.Context, a review.Asker, rep Report, p Proposals) (Judged, error) {
	nodes := map[string]Node{}
	for _, n := range rep.Nodes {
		nodes[n.ID] = n
	}
	var j Judged
	asked := 0
	seen := map[handin.ReportFact]bool{}
	for _, f := range p.Facts {
		if seen[f] {
			continue
		}
		if asked == MaxJevQuestions {
			break
		}
		seen[f] = true
		asked++
		item := f.Subject + " " + f.Relation + " " + f.Object
		ps, err := a.Noul(ctx, review.ItemState("Report", item, []string{f.Quote, nodes[f.SourceNode].Quote}, review.MaxState),
			map[string]string{"holds": factQuestion})
		if err != nil {
			return Judged{}, err
		}
		if ps["holds"] >= review.PassP {
			j.Facts = append(j.Facts, JudgedFact{f, ps["holds"]})
		}
	}
	for _, s := range p.Links {
		if asked == MaxJevQuestions || rep.HandIn == nil {
			break
		}
		asked++
		n := nodes[s.Node]
		item := s.Phrase + "\n" + sentence(rep.HandIn.Text, s.Start, s.End)
		ps, err := a.Noul(ctx, review.ItemState("Report", item, []string{n.Kind + "\n" + n.Label + "\n" + n.Quote}, review.MaxState),
			map[string]string{"refers": linkQuestion})
		if err != nil {
			return Judged{}, err
		}
		if ps["refers"] >= review.PassP {
			j.Links = append(j.Links, JudgedSpan{s, ps["refers"]})
		}
	}
	used := map[string]bool{}
	for _, f := range j.Facts {
		used[f.Subject] = true
		if f.Relation != "mentioned_in" {
			used[f.Object] = true
		}
	}
	for _, c := range p.Concepts {
		if used[c.Key] {
			j.Concepts = append(j.Concepts, c)
			used[c.Key] = false
		}
	}
	return j, nil
}

// sentence is the sentence of text holding bytes start to end.
func sentence(text string, start, end int) string {
	from := strings.LastIndexAny(text[:start], ".!?\n") + 1
	to := len(text)
	if i := strings.IndexAny(text[end:], ".!?\n"); i >= 0 {
		to = end + i + 1
	}
	return strings.TrimSpace(text[from:to])
}

type FindingCheck struct {
	Node, CheckID, Question, Choice, Status, Evidence string
	P                                                 float64
}

// CheckFindings asks Jev whether each finding's supporting figures and
// tables, by their recorded text, match it, and how each Grounding paper's
// abstract stands to it. texts holds each output's text by node id and each
// paper's abstract under "paper:<node id>"; an output or paper with no text
// is not asked about.
func CheckFindings(ctx context.Context, a review.Asker, rep Report, texts map[string]string) ([]FindingCheck, error) {
	nodes := map[string]Node{}
	var papers []Node
	for _, n := range rep.Nodes {
		nodes[n.ID] = n
		if n.Kind == "paper" {
			papers = append(papers, n)
		}
	}
	var out []FindingCheck
	for _, f := range rep.Nodes {
		if f.Kind != "finding" {
			continue
		}
		for _, e := range rep.Edges {
			o := nodes[e.From]
			if e.To != f.ID || e.Kind != "supports" || o.Kind != "output" {
				continue
			}
			kind, q := outputKind(o.ContentType)
			text := texts[o.ID]
			if kind == "" || text == "" {
				continue
			}
			ps, err := a.Noul(ctx, review.ItemState("Report", f.Quote, []string{text}, review.MaxState), map[string]string{"matches": q})
			if err != nil {
				return nil, err
			}
			p := ps["matches"]
			out = append(out, FindingCheck{Node: f.ID, CheckID: kind + ":" + o.ID, Question: q, Status: status(p), Evidence: evidence(text), P: p})
		}
		for _, pp := range papers {
			text := texts["paper:"+pp.ID]
			if text == "" {
				continue
			}
			ans, err := a.Choice(ctx, review.ItemState("Report", f.Quote, []string{text}, review.MaxState), noveltyQuestion, noveltyOptions)
			if err != nil {
				return nil, err
			}
			if _, ok := noveltyOptions[ans.Choice]; !ok {
				return nil, fmt.Errorf("jev chose %q, not one of the options", ans.Choice)
			}
			p := ans.Probabilities[ans.Choice]
			s := review.Pass
			if p < review.PassP {
				s = review.Unclear
			}
			out = append(out, FindingCheck{Node: f.ID, CheckID: "novelty:" + pp.ID, Question: noveltyQuestion, Choice: ans.Choice,
				Status: s, Evidence: evidence(text), P: p})
		}
	}
	return out, nil
}

// outputKind says whether an output of contentType is a figure or a table,
// and the question asked of it; "" for neither.
func outputKind(contentType string) (string, string) {
	mt, _, _ := strings.Cut(contentType, ";")
	mt = strings.ToLower(strings.TrimSpace(mt))
	switch {
	case strings.HasPrefix(mt, "image/") || mt == "application/pdf":
		return "figure", figureQuestion
	case mt == "text/csv" || mt == "text/tab-separated-values":
		return "table", tableQuestion
	}
	return "", ""
}

// status is pass when Jev's yes reaches the pass threshold, fail when its no
// does, and unclear otherwise.
func status(p float64) string {
	switch {
	case p >= review.PassP:
		return review.Pass
	case 1-p >= review.PassP:
		return review.Fail
	}
	return review.Unclear
}

// evidence is the text a check keeps, cut as a fence cuts it.
func evidence(s string) string {
	if len(s) > fence.Max {
		return strings.ToValidUTF8(s[:fence.Max], "")
	}
	return s
}
