package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"lykeion/server/internal/fence"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/review"
)

// The questions one report asks Jev, in fixed budgets: JudgeQuestions for
// its proposals, facts first, and CheckQuestions for its findings' checks,
// figures and tables first. Anything past a budget is not asked: a proposal
// is not kept, and a check is not stored.
const (
	JudgeQuestions  = 120
	CheckQuestions  = 80
	MaxJevQuestions = JudgeQuestions + CheckQuestions
)

const (
	factQuestion         = "Does the evidence state that the item holds? Answer yes only if the quoted evidence says so."
	linkQuestion         = "Does the quoted phrase, in the item's sentence, refer to the node the evidence describes?"
	imageFigureQuestion  = "Does this figure show what the claim says?"
	textFigureQuestion   = "Does this figure, as its recorded text describes it, show what the claim says?"
	scriptFigureQuestion = "Does this figure, as the script that produced it describes it, show what the claim says?"
	tableQuestion        = "Does this table's data match the claim?"
	noveltyQuestion      = "Reading only the paper's abstract, does it confirm the finding, contradict it, or is it unrelated to it?"
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
// evidence, up to JudgeQuestions, and keeps those it accepts and the
// concepts a kept fact still mentions, each once. Identical facts are
// asked and kept once.
func Judge(ctx context.Context, a review.Asker, rep Report, p Proposals) (Judged, error) {
	nodes := map[string]Node{}
	for _, n := range rep.Nodes {
		nodes[n.ID] = n
	}
	concepts := map[string]handin.ReportConcept{}
	for _, c := range p.Concepts {
		if _, ok := concepts[c.Key]; !ok {
			concepts[c.Key] = c
		}
	}
	var j Judged
	asked := 0
	seen := map[handin.ReportFact]bool{}
	for _, f := range p.Facts {
		if seen[f] {
			continue
		}
		if asked == JudgeQuestions {
			break
		}
		seen[f] = true
		asked++
		subject := conceptText(concepts[f.Subject])
		item := subject + " " + strings.ReplaceAll(f.Relation, "_", " ") + " " + conceptText(concepts[f.Object])
		evidence := []string{f.Quote, nodes[f.SourceNode].Quote}
		if f.Relation == "mentioned_in" {
			// The node is named by its kind and label, never its id, and its
			// own recorded text, under that same name, stands beside the
			// fact's quote.
			n := nodes[f.Object]
			item = fmt.Sprintf("%s is mentioned in %s %q", subject, n.Kind, n.Label)
			evidence = []string{f.Quote, fmt.Sprintf("%s %q: %s", n.Kind, n.Label, nodeText(n))}
			if f.SourceNode != f.Object {
				evidence = append(evidence, nodes[f.SourceNode].Quote)
			}
		}
		ps, err := a.Noul(ctx, review.ItemState("Report", item, evidence, review.MaxState),
			map[string]string{"holds": factQuestion})
		if err != nil {
			return Judged{}, err
		}
		if ps["holds"] >= review.PassP {
			j.Facts = append(j.Facts, JudgedFact{f, ps["holds"]})
		}
	}
	for _, s := range p.Links {
		if asked == JudgeQuestions || rep.HandIn == nil {
			break
		}
		asked++
		n := nodes[s.Node]
		item := fmt.Sprintf("Does %q in this sentence refer to %s %q?\n%s", s.Phrase, n.Kind, n.Label, sentence(rep.HandIn.Text, s.Start, s.End))
		about := n.Kind + "\n" + n.Label
		if n.Quote != "" {
			about += "\n" + n.Quote
		}
		ps, err := a.Noul(ctx, review.ItemState("Report", item, []string{about}, review.MaxState),
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

// conceptText is how a fact names a concept to Jev: its label and type,
// as part of the judged item, never as evidence.
func conceptText(c handin.ReportConcept) string {
	return c.Label + " (" + c.Type + ")"
}

// nodeText is a node's own recorded text: its quote, or its label when it
// has none.
func nodeText(n Node) string {
	if n.Quote != "" {
		return n.Quote
	}
	return n.Label
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
	Basis                                             string // image|text|script: what Jev judged
	P                                                 float64
}

// ImageAsker is an Asker that can also show Jev images.
type ImageAsker interface {
	review.Asker
	NoulImages(ctx context.Context, state string, questions map[string]string, images map[string]jev.Image) (map[string]float64, error)
}

// Evidence is what a report's checks are judged from, by node id: each
// output's fitted image, its saved text and the script that produced it,
// and under "paper:<node id>" each paper's abstract.
type Evidence struct {
	Images  map[string]jev.Image
	Texts   map[string]string
	Scripts map[string]string
}

// CheckFindings asks Jev, up to CheckQuestions, whether each finding's
// supporting figures and tables match it, and then how each Grounding
// paper's abstract stands to it. A PNG or JPEG figure is judged from its
// image; any other figure from its saved text, or else from the script that
// produced it. A table is judged from its text. An output or paper with
// nothing to judge from is not asked about.
func CheckFindings(ctx context.Context, a ImageAsker, rep Report, ev Evidence) ([]FindingCheck, error) {
	nodes := map[string]Node{}
	var papers, findings []Node
	for _, n := range rep.Nodes {
		nodes[n.ID] = n
		switch n.Kind {
		case "paper":
			papers = append(papers, n)
		case "finding":
			findings = append(findings, n)
		}
	}
	var asks []func() (FindingCheck, error)
	for _, f := range findings {
		for _, e := range rep.Edges {
			o := nodes[e.From]
			if e.To != f.ID || e.Kind != "supports" || o.Kind != "output" {
				continue
			}
			if ask := outputAsk(ctx, a, f, o, ev); ask != nil {
				asks = append(asks, ask)
			}
		}
	}
	for _, f := range findings {
		for _, pp := range papers {
			if text := ev.Texts["paper:"+pp.ID]; text != "" {
				asks = append(asks, func() (FindingCheck, error) { return novelty(ctx, a, f, pp, text) })
			}
		}
	}
	var out []FindingCheck
	for _, ask := range asks[:min(len(asks), CheckQuestions)] {
		c, err := ask()
		if errors.Is(err, errNoCheck) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// outputAsk is the question about output o's support for finding f, or nil
// when there is nothing to judge it from.
func outputAsk(ctx context.Context, a ImageAsker, f, o Node, ev Evidence) func() (FindingCheck, error) {
	kind := outputKind(o.ContentType)
	if im, ok := ev.Images[o.ID]; ok && kind == "image" {
		fallback := textAsk(ctx, a, f, o, ev, "figure")
		return func() (FindingCheck, error) {
			c := FindingCheck{Node: f.ID, CheckID: "figure:" + o.ID, Basis: "image", Question: imageFigureQuestion}
			ref := uuid.NewString()
			state := review.ItemState("Report", f.Quote, nil, review.MaxState) + "\nThe figure is the image " + ref + ".\n"
			ps, err := a.NoulImages(ctx, state, map[string]string{"matches": c.Question}, map[string]jev.Image{ref: im})
			switch {
			case errors.Is(err, jev.ErrRefused) || errors.Is(err, jev.ErrTooLong):
				if fallback == nil {
					return FindingCheck{}, errNoCheck
				}
				return fallback()
			case err != nil:
				return FindingCheck{}, err
			}
			c.P = ps["matches"]
			c.Status = review.StatusOf(c.P)
			return c, nil
		}
	}
	if kind == "image" {
		kind = "figure"
	}
	return textAsk(ctx, a, f, o, ev, kind)
}

// errNoCheck is an output that, once Jev refused its image, has nothing
// else to be judged from.
var errNoCheck = errors.New("no check")

// textAsk is the question about output o's support for finding f judged
// from its saved text, or a figure's producing script, or nil when there is
// nothing to judge it from.
func textAsk(ctx context.Context, a ImageAsker, f, o Node, ev Evidence, kind string) func() (FindingCheck, error) {
	c := FindingCheck{Node: f.ID, CheckID: "figure:" + o.ID}
	var evidence string
	switch kind {
	case "figure":
		switch {
		case ev.Texts[o.ID] != "":
			c.Basis, c.Question, evidence = "text", textFigureQuestion, ev.Texts[o.ID]
		case ev.Scripts[o.ID] != "":
			c.Basis, c.Question, evidence = "script", scriptFigureQuestion, ev.Scripts[o.ID]
		default:
			return nil
		}
	case "table":
		if ev.Texts[o.ID] == "" {
			return nil
		}
		c.CheckID, c.Basis, c.Question, evidence = "table:"+o.ID, "text", tableQuestion, ev.Texts[o.ID]
	default:
		return nil
	}
	return func() (FindingCheck, error) {
		ps, err := a.Noul(ctx, review.ItemState("Report", f.Quote, []string{evidence}, review.MaxState), map[string]string{"matches": c.Question})
		if err != nil {
			return FindingCheck{}, err
		}
		c.P, c.Evidence = ps["matches"], cut(evidence)
		c.Status = review.StatusOf(c.P)
		return c, nil
	}
}

// novelty asks how paper pp's abstract stands to finding f.
func novelty(ctx context.Context, a review.Asker, f, pp Node, text string) (FindingCheck, error) {
	ans, err := a.Choice(ctx, review.ItemState("Report", f.Quote, []string{text}, review.MaxState), noveltyQuestion, noveltyOptions)
	if err != nil {
		return FindingCheck{}, err
	}
	if _, ok := noveltyOptions[ans.Choice]; !ok {
		return FindingCheck{}, fmt.Errorf("jev chose %q, not one of the options", ans.Choice)
	}
	p := ans.Probabilities[ans.Choice]
	s := review.Pass
	if p < review.PassP {
		s = review.Unclear
	}
	return FindingCheck{Node: f.ID, CheckID: "novelty:" + pp.ID, Question: noveltyQuestion, Choice: ans.Choice,
		Status: s, Evidence: cut(text), Basis: "text", P: p}, nil
}

// outputKind is "image" for a PNG or JPEG Jev can look at, "figure" for any
// other figure, "table" for a table, and "" for neither.
func outputKind(contentType string) string {
	mt, _, _ := strings.Cut(contentType, ";")
	mt = strings.ToLower(strings.TrimSpace(mt))
	switch {
	case mt == "image/png" || mt == "image/jpeg" || mt == "image/jpg":
		return "image"
	case strings.HasPrefix(mt, "image/") || mt == "application/pdf":
		return "figure"
	case mt == "text/csv" || mt == "text/tab-separated-values":
		return "table"
	}
	return ""
}

// ImageFormat is the decoded format an output of contentType must have to
// be shown to Jev, or "" when it is not a PNG or JPEG.
func ImageFormat(contentType string) string {
	if outputKind(contentType) != "image" {
		return ""
	}
	if strings.Contains(strings.ToLower(contentType), "png") {
		return "png"
	}
	return "jpeg"
}

// cut is the text a check keeps, cut as a fence cuts it.
func cut(s string) string {
	if len(s) > fence.Max {
		return strings.ToValidUTF8(s[:fence.Max], "")
	}
	return s
}
