package sources

import (
	"context"
	"fmt"
	"io"
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

// maxProbe is the most of a link's GET body read before it is dropped.
const maxProbe = 4 << 10

// probe asks a dataset link with method and returns its status.
func probe(ctx context.Context, method, link string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, link, nil)
	if err != nil {
		return 0, err
	}
	resp, err := linkClient.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxProbe))
	resp.Body.Close()
	return resp.StatusCode, nil
}

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
	// Europe PMC often has no abstract for an older paper that OpenAlex has
	// by its DOI. The OpenAlex record is the same paper, so its exclusion
	// holds too; an OpenAlex that can't answer leaves the abstract empty.
	if err == nil && d.Abstract == "" && d.DOI != "" && strings.HasPrefix(id, "pmid:") {
		if oa, oaErr := l.OpenAlex.Get(ctx, "doi:"+d.DOI); oaErr == nil {
			if l.Exclude.Hides(oa.Hit) {
				return Hit{}, fmt.Errorf("paper %s: %w", id, ErrNotFound)
			}
			d.Abstract = oa.Abstract
		}
	}
	return d.Hit, err
}

// Dataset takes a GEO accession, or an https link that answers with 2xx. The
// agent chose the link, so a link that is not https, names no host, leads to
// an address the server may not reach, or does not answer with 2xx is
// ErrNotFound, never an outage.
func (l Lookup) Dataset(ctx context.Context, accession string) (Hit, error) {
	if strings.Contains(accession, "://") {
		if !strings.HasPrefix(accession, "https://") {
			return Hit{}, fmt.Errorf("dataset link %q: want https: %w", accession, ErrNotFound)
		}
		code, err := probe(ctx, http.MethodHead, accession)
		// Some servers answer only GET.
		if err == nil && (code == http.StatusMethodNotAllowed || code == http.StatusNotImplemented) {
			code, err = probe(ctx, http.MethodGet, accession)
		}
		if ctx.Err() != nil {
			return Hit{}, ctx.Err()
		}
		if err != nil {
			return Hit{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		if code < 200 || code > 299 {
			return Hit{}, fmt.Errorf("dataset link %s: %d %s: %w", accession, code, http.StatusText(code), ErrNotFound)
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
