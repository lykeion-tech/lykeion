package handler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/records"
	"lykeion/server/internal/report"
	"lykeion/server/internal/review"
	"lykeion/server/internal/sources"
	db "lykeion/server/pkg/db/generated"
)

// resolveReportPaperTitles changes display labels only; recorded IDs and graph
// construction remain independent of external literature services.
func (h *H) resolveReportPaperTitles(ctx context.Context, rep *report.Report, recs []db.Record) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	papers := map[string]review.PaperBody{}
	cited := map[string]bool{}
	for _, rec := range recs {
		if rec.Kind != review.KindPaper || rec.SupersededAt != nil {
			continue
		}
		p := records.Body[review.PaperBody](rec)
		papers["rec:"+rec.Key] = p
		for _, cite := range p.Cites {
			if k, ok := strings.CutPrefix(cite, "call:"); ok {
				cited[k] = true
			}
		}
	}
	var calls []db.Record
	for _, rec := range recs {
		if rec.Kind == "call" && rec.SupersededAt == nil && cited[rec.Key] {
			calls = append(calls, rec)
		}
	}
	answers := map[string]flow.Call{}
	if h.Blobs != nil {
		answers, _ = flow.LoadCalls(ctx, h.Blobs, calls)
	}
	if answers == nil {
		answers = map[string]flow.Call{}
	}
	for _, rec := range calls {
		if _, ok := answers[rec.Key]; !ok {
			var body struct {
				Answer string `json:"answer"`
				Error  string `json:"error"`
			}
			if json.Unmarshal(rec.Body, &body) == nil && body.Error == "" {
				answers[rec.Key] = flow.Call{Answer: body.Answer}
			}
		}
	}
	unresolved := map[string][]int{}
	for i := range rep.Nodes {
		n := &rep.Nodes[i]
		p, ok := papers[n.ID]
		if n.Kind != "paper" || !ok || strings.TrimSpace(p.ID) == "" {
			continue
		}
		n.PaperID = p.ID
		for _, cite := range p.Cites {
			k, ok := strings.CutPrefix(cite, "call:")
			if !ok {
				continue
			}
			if title := recordedPaperTitle(answers[k].Answer, p.ID); title != "" {
				n.Label = title
				break
			}
		}
		if n.Label == p.ID {
			unresolved[p.ID] = append(unresolved[p.ID], i)
		}
	}
	if h.Engine == nil || h.Engine.Sources == nil || len(unresolved) == 0 {
		return
	}
	type result struct{ id, title string }
	jobs := make(chan string)
	results := make(chan result, len(unresolved))
	var wg sync.WaitGroup
	for i := 0; i < min(4, len(unresolved)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				hit, err := h.Engine.Sources.Paper(ctx, id)
				if err == nil && strings.TrimSpace(hit.Title) != "" {
					results <- result{id, strings.TrimSpace(hit.Title)}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for id := range unresolved {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	for result := range results {
		for _, i := range unresolved[result.id] {
			rep.Nodes[i].Label = result.title
		}
	}
}

// Only an exact recorded identifier binds a search result to a paper.
func recordedPaperTitle(answer, id string) string {
	var value any
	if json.Unmarshal([]byte(answer), &value) != nil {
		return ""
	}
	var find func(any) string
	find = func(v any) string {
		switch v := v.(type) {
		case []any:
			for _, item := range v {
				if title := find(item); title != "" {
					return title
				}
			}
		case map[string]any:
			raw, _ := json.Marshal(v)
			var hit sources.Hit
			if json.Unmarshal(raw, &hit) == nil && strings.TrimSpace(hit.Title) != "" {
				match := func(s string) bool { return s != "" && strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(id)) }
				if match(hit.ID) || match("pmid:"+hit.PMID) && hit.PMID != "" || match("doi:"+hit.DOI) && hit.DOI != "" {
					return strings.TrimSpace(hit.Title)
				}
			}
			for _, item := range v {
				if title := find(item); title != "" {
					return title
				}
			}
		}
		return ""
	}
	return find(value)
}
