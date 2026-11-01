package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/sources"
	db "lykeion/server/pkg/db/generated"
)

type GroundingInput struct {
	Question string
	Criteria []string
	Items    []db.Record // this round's paper, dataset and no_qualifying records
	Carried  []Item      // items that passed on an earlier attempt
	Attempt  int
}

// maxCandidates bounds the uncited recall hits Jev screens.
const maxCandidates = 20

const weakestInstructions = "Which of these sources is least supported as meeting the research question's conditions?"

// criteriaQuestions are the per-criterion Noul questions. A study with no
// criteria is screened against the research question itself.
func criteriaQuestions(question string, criteria []string) map[string]string {
	if len(criteria) == 0 {
		return map[string]string{"criterion:1": "Does this source bear directly on the research question as it was asked: " +
			strings.TrimSuffix(strings.TrimSpace(question), "?") + "?"}
	}
	qs := make(map[string]string, len(criteria))
	for i, c := range criteria {
		qs[fmt.Sprintf("criterion:%d", i+1)] = "Does this source meet this condition of the research question: " + c + "?"
	}
	return qs
}

// screen asks Jev every criterion about one source. Jev reads only the
// source itself, never who declared it.
func screen(ctx context.Context, a Asker, qs map[string]string, label, title, text, abstract string) ([]Check, error) {
	state := ItemState("Grounding", label+"\n"+title+"\n"+text, []string{abstract}, MaxState)
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
		cs = append(cs, Check{ID: id, By: "jev", Question: qs[id], P: &p, Status: StatusOf(p)})
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

// citeKeys are the forms a cited paper may take in a search hit: its id and
// DOI, compared without case. An arXiv id also stands for its arXiv DOI.
func citeKeys(id string) []string {
	id = strings.ToLower(id)
	kind, ref, _ := strings.Cut(id, ":")
	switch kind {
	case "doi":
		return []string{id, ref}
	case "arxiv":
		return []string{id, "10.48550/arxiv." + ref}
	}
	return []string{id}
}

// Grounding reviews a Grounding column: the server's checks on each source,
// Jev's per-criterion questions on those that pass, a recall search for
// papers the agents left out, and Jev's pick of the weakest source.
func Grounding(ctx context.Context, a Asker, s Sources, in GroundingInput) (Result, error) {
	r := Result{Column: board.Grounding, Attempt: in.Attempt}
	if len(in.Items) == 0 && len(in.Carried) == 0 {
		return r, nil
	}
	qs := criteriaQuestions(in.Question, in.Criteria)
	cited := map[string]bool{}
	cite := func(ids ...string) {
		for _, id := range ids {
			for _, k := range citeKeys(id) {
				cited[k] = true
			}
		}
	}
	for _, c := range in.Carried {
		cite(c.Label)
	}

	var asked []Item
	var askedText []string
	noQualifying := -1
	for _, rec := range in.Items {
		it := Item{Key: rec.Key, Kind: rec.Kind}
		var title, text, abstract string
		var hit sources.Hit
		var err error
		switch rec.Kind {
		case KindPaper:
			var b PaperBody
			if err := json.Unmarshal(rec.Body, &b); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			it.Label, text = b.ID, b.Text
			hit, err = s.Paper(ctx, b.ID)
			cite(b.ID)
		case KindDataset:
			var b DatasetBody
			if err := json.Unmarshal(rec.Body, &b); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			it.Label, text = b.Accession, b.Text
			hit, err = s.Dataset(ctx, b.Accession)
		case KindNoQualifying:
			var b NoQualifyingBody
			if err := json.Unmarshal(rec.Body, &b); err != nil {
				return Result{}, fmt.Errorf("record %s: %w", rec.Key, err)
			}
			it.Label = string([]rune(b.Text)[:min(80, len([]rune(b.Text)))])
			noQualifying = len(r.Items)
			r.Items = append(r.Items, it)
			continue
		default:
			continue
		}
		if err == nil && hit.ID != "" {
			cite(hit.ID)
			if hit.DOI != "" {
				cite("doi:" + hit.DOI)
			}
			title, abstract = hit.Title, hit.Abstract
		}
		it.Checks = served(hit, err)
		if ItemStatus(it.Checks) == Pass {
			cs, err := screen(ctx, a, qs, it.Label, title, text, abstract)
			if err != nil {
				return Result{}, err
			}
			it.Checks = append(it.Checks, cs...)
			asked = append(asked, it)
			askedText = append(askedText, it.Key+": "+it.Label+"\n"+title+"\n"+text)
		}
		it.Status = ItemStatus(it.Checks)
		r.Items = append(r.Items, it)
	}

	query := strings.TrimSpace(in.Question + " " + strings.Join(in.Criteria, " "))
	hits, err := s.Search(ctx, query)
	if err != nil {
		return Result{}, fmt.Errorf("recall search: %w", err)
	}
	n := 0
	for _, h := range hits {
		if n == maxCandidates {
			break
		}
		if h.Retracted || cited[strings.ToLower(h.ID)] || (h.DOI != "" && cited[strings.ToLower(h.DOI)]) {
			continue
		}
		n++
		label := h.DOI
		if label == "" {
			label = h.ID
		}
		cs, err := screen(ctx, a, qs, label, h.Title, "", h.Abstract)
		if err != nil {
			return Result{}, err
		}
		if ItemStatus(cs) == Pass {
			cs = append(cs, Check{ID: "missed", By: "server", Detail: "meets every criterion and was not cited", Status: Fail})
			r.Missed = append(r.Missed, Item{Kind: KindPaper, Label: label, Status: Fail, Checks: cs})
		}
	}

	if noQualifying >= 0 {
		c := Check{ID: "none_qualify", By: "server", Status: Pass}
		if len(r.Missed) > 0 {
			c.Status, c.Detail = Fail, fmt.Sprintf("%d papers meet every criterion", len(r.Missed))
		}
		r.Items[noQualifying].Checks = []Check{c}
		r.Items[noQualifying].Status = c.Status
	}

	for _, c := range in.Carried {
		c.Carried = true
		r.Items = append(r.Items, c)
	}

	if len(asked) >= 2 {
		options := make(map[string]string, len(asked))
		for _, it := range asked {
			options[it.Key] = it.Label
		}
		ans, err := a.Choice(ctx, ItemState("Grounding", strings.Join(askedText, "\n\n"), nil, MaxState), weakestInstructions, options)
		if err != nil {
			return Result{}, err
		}
		r.Weakest = &Weakest{Key: ans.Choice, P: ans.Probabilities[ans.Choice]}
	}
	return r, nil
}
