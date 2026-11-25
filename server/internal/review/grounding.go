package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"lykeion/server/internal/board"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/sources"
	db "lykeion/server/pkg/db/generated"
)

type GroundingInput struct {
	Question string
	Criteria []string
	Items    []db.Record       // this round's paper, dataset and no_qualifying records
	Comments []string          // the study's researcher comments, newest last, at most 20
	Declined map[string]string // normalised paper id -> the Team's reason for declining it
	Carried  []Item            // items that passed on an earlier attempt
	Attempt  int
}

// maxCandidates bounds the uncited recall hits Jev screens.
const maxCandidates = 20

// outOfScopeQuestion is asked about a missed paper the Team declined; yes
// means the decline stands.
const outOfScopeQuestion = "Given the research question and the researcher's comments, is this paper outside the study's scope?"

// maxComments bounds the researcher comments that reach Jev.
const maxComments = 20

const weakestInstructions = "Which of these sources is least supported as meeting the research question's conditions?"

// criteriaQuestions are the per-criterion Noul questions. A study with no
// criteria is screened against the research question itself, which reaches
// Jev fenced in the state: people write it.
func criteriaQuestions(criteria []string) map[string]string {
	if len(criteria) == 0 {
		return map[string]string{"criterion:1": "Does this source bear directly on the research question as it was asked?"}
	}
	qs := make(map[string]string, len(criteria))
	for i, c := range criteria {
		qs[fmt.Sprintf("criterion:%d", i+1)] = "Does this source meet this condition of the research question: " + c + "?"
	}
	return qs
}

// screen asks Jev every criterion about one source, with the question's
// fenced context first. Jev reads only the source itself, never who declared it.
// after is more fenced context appended to the state.
func screen(ctx context.Context, a Asker, qs map[string]string, framing []string, label, title, text, abstract, after string) ([]Check, error) {
	state := ItemState("Grounding", label+"\n"+title+"\n"+text, append(slices.Clone(framing), abstract), MaxState) + after
	ps, err := a.Noul(ctx, state, qs)
	if err != nil {
		return nil, err
	}
	var cs []Check
	for i := 1; i <= len(qs); i++ {
		id := fmt.Sprintf("criterion:%d", i)
		p, ok := ps[id]
		if !ok {
			return nil, fmt.Errorf("jev gave no answer for %s", id)
		}
		status := StatusOf(p)
		p = stored(p)
		cs = append(cs, Check{ID: id, By: "jev", Question: qs[id], P: &p, Status: status})
	}
	return cs, nil
}

// served are the server's checks on a resolved source.
func served(h sources.Hit, err error) []Check {
	if err != nil || h.ID == "" {
		detail := "the lookup found nothing"
		if err != nil {
			detail = err.Error()
		}
		return []Check{{ID: "exists", By: "server", Detail: detail, Status: Fail}}
	}
	cs := []Check{{ID: "exists", By: "server", Detail: h.ID, Status: Pass}}
	if h.Retracted {
		return append(cs, Check{ID: "not_retracted", By: "server", Detail: "marked retracted", Status: Fail})
	}
	return append(cs, Check{ID: "not_retracted", By: "server", Status: Pass})
}

// citeKeys are the forms a declared paper id takes among search hits'
// identities. An arXiv id also stands for its arXiv DOI.
func citeKeys(id string) []string {
	id = strings.ToLower(id)
	if ref, ok := strings.CutPrefix(id, "arxiv:"); ok {
		return []string{id, "doi:10.48550/arxiv." + ref}
	}
	return []string{id}
}

// hitKeys are a resolved source's identities: its id, DOI and PMID, without case.
func hitKeys(h sources.Hit) []string {
	keys := []string{strings.ToLower(h.ID)}
	if h.DOI != "" {
		keys = append(keys, "doi:"+strings.ToLower(h.DOI))
	}
	if h.PMID != "" {
		keys = append(keys, "pmid:"+h.PMID)
	}
	return keys
}

// resolved is a lookup's hit, with not-found told apart from an upstream
// that could not answer: only the second stops the review.
func resolved(h sources.Hit, err error) (sources.Hit, error, error) {
	if err != nil && !errors.Is(err, sources.ErrNotFound) {
		return h, nil, err
	}
	return h, err, nil
}

// stopwords are the common English words a keyword search leaves out.
var stopwords = strings.Fields(`a about after all also an and any are as at be been before between both but by can could
did do does during each for from had has have how if in into is it its may might more most no not of on or other over
should so such than that the their them then there these they this those through to under up was we were what when where
whether which while who whom why will with within without would`)

// keywords are the question's words, punctuation removed and stopwords
// left out, at most 8 of them.
func keywords(q string) string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(out) == 8 {
			break
		}
		if !slices.Contains(stopwords, w) {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// Keep OpenAlex recall focused on the opening question. The full brief is
// still used for judging evidence.
func recallQuery(brief string) string {
	const maxSearchRunes = 480
	query := strings.TrimSpace(brief)
	runes := []rune(query)
	if len(runes) <= maxSearchRunes {
		return query
	}
	// A titled study puts its title above the brief. Skip it when it is
	// a heading so keyword fallback starts with the research question.
	if title, body, ok := strings.Cut(query, "\n\n"); ok &&
		len(title) < maxSearchRunes/2 && !strings.ContainsAny(title, "\n?") {
		query = strings.TrimSpace(body)
	}
	query = strings.TrimSpace(strings.TrimPrefix(query, "Question:"))
	runes = []rune(query)
	if len(runes) <= maxSearchRunes {
		return query
	}
	prefix := string(runes[:maxSearchRunes])
	if end := strings.IndexByte(prefix, '?'); end >= 0 {
		prefix = prefix[:end+1]
	} else if end := strings.LastIndexFunc(prefix, unicode.IsSpace); end > 0 {
		prefix = prefix[:end]
	}
	return strings.Join(strings.Fields(prefix), " ")
}

// Grounding reviews a Grounding column: the server's checks on each source,
// Jev's per-criterion questions on those that pass, a recall search for
// papers the agents left out, and Jev's pick of the weakest source.
func Grounding(ctx context.Context, a Asker, s Sources, in GroundingInput) (Result, error) {
	r := Result{Column: board.Grounding, Attempt: in.Attempt}
	if len(in.Items) == 0 && len(in.Carried) == 0 {
		return r, nil
	}
	qs := criteriaQuestions(in.Criteria)
	var asking []string
	if len(in.Criteria) == 0 {
		asking = []string{"Research question: " + in.Question}
	}
	var commentsBlock string
	var checksErr error
	if cs := in.Comments; len(cs) > 0 {
		commentsBlock = fence.Untrusted("Researcher's comments", strings.Join(cs[max(0, len(cs)-maxComments):], "\n"))
	}
	cited := map[string]bool{}
	cite := func(keys []string) {
		for _, k := range keys {
			cited[k] = true
		}
	}
	// A carried paper is resolved again so that every identity it has
	// upstream counts as cited, not only the id the agent wrote.
	for _, c := range in.Carried {
		// A missed paper whose decline stood is not found missing again.
		if c.Kind == KindDeclined {
			cite([]string{c.Identity})
			continue
		}
		if c.Kind != KindPaper || c.Status == Withdrawn {
			continue
		}
		cite(citeKeys(c.Label))
		h, missing, err := resolved(s.Paper(ctx, c.Label))
		if err != nil {
			checksErr = errors.Join(checksErr, fmt.Errorf("%s %s: %w", c.Kind, c.Label, err))
			continue
		}
		if missing == nil && h.ID != "" {
			cite(hitKeys(h))
		}
	}

	var asked []Item
	var askedText []string
	var noQualifying []int
	for _, rec := range in.Items {
		it := Item{Key: rec.Key, Kind: rec.Kind, Identity: IdentityOf(rec)}
		var title, text, abstract string
		var hit sources.Hit
		var missing, err error
		switch rec.Kind {
		case KindPaper:
			var b PaperBody
			if err := json.Unmarshal(rec.Body, &b); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			it.Label, text = b.ID, b.Text
			hit, missing, err = resolved(s.Paper(ctx, b.ID))
			cite(citeKeys(b.ID))
		case KindDataset:
			var b DatasetBody
			if err := json.Unmarshal(rec.Body, &b); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			it.Label, text = b.Accession, b.Text
			hit, missing, err = resolved(s.Dataset(ctx, b.Accession))
		case KindNoQualifying:
			var b NoQualifyingBody
			if err := json.Unmarshal(rec.Body, &b); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			it.Label = LabelOf(rec)
			noQualifying = append(noQualifying, len(r.Items))
			r.Items = append(r.Items, it)
			continue
		default:
			continue
		}
		if err != nil {
			checksErr = errors.Join(checksErr, fmt.Errorf("%s %s: %w", rec.Kind, it.Label, err))
			it.Checks = []Check{{ID: "exists", By: "server", Detail: "source lookup unavailable", Status: Unclear}}
			it.Status, it.Unjudged = Unclear, true
			r.Items = append(r.Items, it)
			continue
		}
		if missing == nil && hit.ID != "" {
			cite(hitKeys(hit))
			title, abstract = hit.Title, hit.Abstract
		}
		it.Checks = served(hit, missing)
		if ItemStatus(it.Checks) == Pass && a == nil {
			r.AwaitJudgment(&it)
		} else if ItemStatus(it.Checks) == Pass {
			cs, err := screen(ctx, a, qs, asking, it.Label, title, text, abstract, "")
			if err != nil {
				return Result{}, err
			}
			it.Checks = append(it.Checks, cs...)
			asked = append(asked, it)
			askedText = append(askedText, it.Label+"\n"+title+"\n"+text)
		}
		it.Status = ItemStatus(it.Checks)
		r.Items = append(r.Items, it)
	}

	// If the search finds nothing, retry with keywords. An empty search is
	// reported in the Result and does not fail the column.
	query := recallQuery(in.Question)
	hits, err := s.Search(ctx, query)
	if err == nil && len(hits) == 0 {
		if k := keywords(query); k != "" && k != query {
			query = k
			hits, err = s.Search(ctx, query)
		}
	}
	if err != nil {
		checksErr = errors.Join(checksErr, fmt.Errorf("recall search: %w", err))
		hits = nil
	}
	n := 0
	var unscreened []string
	for _, h := range hits {
		if n+len(unscreened) == maxCandidates {
			break
		}
		if h.Retracted || slices.ContainsFunc(hitKeys(h), func(k string) bool { return cited[k] }) {
			continue
		}
		label := h.ID
		if h.DOI != "" {
			label = "doi:" + h.DOI
		}
		if a == nil {
			unscreened = append(unscreened, label)
			continue
		}
		n++
		cs, err := screen(ctx, a, qs, asking, label, h.Title, "", h.Abstract, commentsBlock)
		if err != nil {
			return Result{}, err
		}
		if ItemStatus(cs) == Pass {
			missed := Check{ID: "missed", By: "server", Detail: "meets every criterion and was not cited", Status: Fail}
			if i := slices.IndexFunc(hitKeys(h), func(k string) bool { _, ok := in.Declined[k]; return ok }); i >= 0 {
				reason := in.Declined[hitKeys(h)[i]]
				state := ItemState("Grounding", label+"\n"+h.Title, append(slices.Clone(asking), h.Abstract, "Reason for declining: "+reason), MaxState) + commentsBlock
				ps, err := a.Noul(ctx, state, map[string]string{"out_of_scope": outOfScopeQuestion})
				if err != nil {
					return Result{}, err
				}
				p, ok := ps["out_of_scope"]
				if !ok {
					return Result{}, fmt.Errorf("jev gave no answer for out_of_scope")
				}
				status := StatusOf(p)
				p = stored(p)
				cs = append(cs, Check{ID: "out_of_scope", By: "jev", Question: outOfScopeQuestion, P: &p, Status: status})
				// A decline Jev accepts stands as a passing item, and one it is
				// unsure of is carried forward as a note; only a rejected one
				// leaves the paper missed.
				if status != Fail {
					r.Items = append(r.Items, Item{Kind: KindDeclined, Label: label, Identity: hitKeys(h)[i], Status: status, Checks: cs})
					continue
				}
				missed.Detail = "meets every criterion; the declining reason did not convince Jev"
			}
			cs = append(cs, missed)
			r.Missed = append(r.Missed, Item{Kind: KindPaper, Label: label, Status: Fail, Checks: cs})
		}
	}
	r.Recall = &Recall{Query: query, Hits: len(hits), Screened: n, Unscreened: unscreened, Unavailable: err != nil}

	for _, i := range noQualifying {
		c := Check{ID: "none_qualify", By: "server", Status: Pass}
		if err != nil {
			c.Status, c.Detail = Unclear, "recall search unavailable"
		} else if len(unscreened) > 0 {
			c.Status, c.Detail = Unclear, "recall candidates were not screened"
			r.Items[i].Unjudged = true
		}
		if len(r.Missed) > 0 {
			c.Status, c.Detail = Fail, fmt.Sprintf("%d papers meet every criterion", len(r.Missed))
		}
		r.Items[i].Checks = []Check{c}
		r.Items[i].Status = c.Status
	}

	for _, c := range in.Carried {
		c.Carried = true
		r.Items = append(r.Items, c)
	}

	if checksErr != nil {
		return r, checksErr
	}

	// Jev's options are neutral names; what each source says, written by
	// agents, reaches Jev only fenced in the state.
	if len(asked) >= 2 {
		options := make(map[string]string, len(asked))
		keys := make(map[string]string, len(asked))
		var names []string
		for i, it := range asked {
			opt, name := fmt.Sprintf("source:%d", i+1), fmt.Sprintf("Source %d", i+1)
			options[opt], keys[opt] = name, it.Key
			names = append(names, fmt.Sprintf("Evidence %d is %s.", i+1, name))
		}
		ans, err := a.Choice(ctx, ItemState("Grounding", strings.Join(names, "\n"), askedText, MaxState), weakestInstructions, options)
		if err != nil {
			return Result{}, err
		}
		key, ok := keys[ans.Choice]
		if !ok {
			return Result{}, fmt.Errorf("jev chose %q, not one of the options", ans.Choice)
		}
		r.Weakest = &Weakest{Key: key, P: stored(ans.Probabilities[ans.Choice])}
	}
	return r, nil
}
