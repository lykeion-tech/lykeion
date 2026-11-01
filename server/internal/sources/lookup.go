package sources

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"lykeion/server/internal/connectors"
)

// Lookup resolves the ids and accessions agents declare, and runs the
// reviewer's own recall search.
type Lookup struct {
	OpenAlex  OpenAlex
	Arxiv     *Arxiv
	EuropePMC EuropePMC
	GEO       GEO
	// Exclude keeps these papers out of every answer, as the hosted sources
	// server does; nil keeps nothing back.
	Exclude *Exclusions
}

// linkClient checks dataset links. Agents choose the links, so it refuses
// private and reserved addresses.
var linkClient = connectors.Guarded()

// maxRecall bounds the recall candidates one search returns.
const maxRecall = 20

// Paper takes "doi:…", "arxiv:…" or "pmid:…". An id no upstream holds, or
// one of no known form, is ErrNotFound; any other error is the upstream's.
func (l Lookup) Paper(ctx context.Context, id string) (Hit, error) {
	var d Doc
	var err error
	switch kind, ref, _ := strings.Cut(id, ":"); {
	case ref == "":
		return Hit{}, fmt.Errorf("paper id %q: want doi:…, arxiv:… or pmid:…: %w", id, ErrNotFound)
	case kind == "doi":
		d, err = l.OpenAlex.Get(ctx, "doi:"+ref)
	case kind == "arxiv" && l.Arxiv != nil:
		d, err = l.Arxiv.Get(ctx, ref)
	case kind == "pmid":
		d, err = l.EuropePMC.Get(ctx, "MED:"+ref)
	default:
		return Hit{}, fmt.Errorf("paper id %q: want doi:…, arxiv:… or pmid:…: %w", id, ErrNotFound)
	}
	if err == nil && (l.Exclude.Match(id) || l.Exclude.Hides(d.Hit)) {
		return Hit{}, fmt.Errorf("paper %s: %w", id, ErrNotFound)
	}
	return d.Hit, err
}

// Dataset takes a GEO accession, or an https link that answers a HEAD with
// 2xx. A link that is not https, answers 4xx, names no host, or leads to an
// address the server may not reach is ErrNotFound: the agent chose it.
func (l Lookup) Dataset(ctx context.Context, accession string) (Hit, error) {
	if strings.Contains(accession, "://") {
		if !strings.HasPrefix(accession, "https://") {
			return Hit{}, fmt.Errorf("dataset link %q: want https: %w", accession, ErrNotFound)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, accession, nil)
		if err != nil {
			return Hit{}, err
		}
		resp, err := linkClient.Do(req)
		if dns := (*net.DNSError)(nil); errors.As(err, &dns) && dns.IsNotFound || errors.Is(err, connectors.ErrRefused) {
			return Hit{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		if err != nil {
			return Hit{}, err
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			return Hit{}, fmt.Errorf("HEAD %s: %s: %w", accession, resp.Status, ErrNotFound)
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return Hit{}, fmt.Errorf("HEAD %s: %s", accession, resp.Status)
		}
		return Hit{ID: accession}, nil
	}
	d, err := l.GEO.OpenWithMetadata(ctx, accession)
	if err != nil {
		return Hit{}, err
	}
	return Hit{ID: accession, Abstract: d.Structure}, nil
}

// Search asks OpenAlex, keeping at most maxRecall hits that are not excluded.
func (l Lookup) Search(ctx context.Context, query string) ([]Hit, error) {
	hits, err := l.OpenAlex.Search(ctx, query)
	hits = slices.DeleteFunc(hits, l.Exclude.Hides)
	return hits[:min(len(hits), maxRecall)], err
}
