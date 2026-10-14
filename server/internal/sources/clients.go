// Package sources talks to literature and dataset APIs and serves them to
// roles as MCP tools that record every call on the card.
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	OpenAlexBase  = "https://api.openalex.org"
	EuropePMCBase = "https://www.ebi.ac.uk/europepmc/webservices/rest"
	EUtilsBase    = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"
)

type Hit struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	DOI       string `json:"doi,omitempty"`
	Year      int    `json:"year,omitempty"`
	Abstract  string `json:"abstract,omitempty"`
	Retracted bool   `json:"retracted,omitempty"`
}

type Doc struct {
	Hit
	Text     string
	FullText bool
}

const (
	// CallTimeout bounds one upstream call, from dial to the last body byte.
	CallTimeout = 30 * time.Second
	// ConnectTimeout bounds reaching the upstream: the dial and the TLS handshake.
	ConnectTimeout = 10 * time.Second
)

var client = newClient(CallTimeout, ConnectTimeout)

func newClient(call, connect time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: connect, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = connect
	return &http.Client{Timeout: call, Transport: t}
}

func get(ctx context.Context, h *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	if h == nil {
		h = client
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return b, err
}

func getJSON(ctx context.Context, h *http.Client, u string, v any) error {
	b, err := get(ctx, h, u)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type OpenAlex struct {
	Base string
	HTTP *http.Client
}

type oaWork struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	DOI       string           `json:"doi"`
	Year      int              `json:"publication_year"`
	Retracted bool             `json:"is_retracted"`
	Inv       map[string][]int `json:"abstract_inverted_index"`
}

func (w oaWork) hit() Hit {
	return Hit{ID: "openalex:" + path.Base(w.ID), Title: w.Title, DOI: strings.TrimPrefix(w.DOI, "https://doi.org/"),
		Year: w.Year, Abstract: invertedAbstract(w.Inv), Retracted: w.Retracted}
}

func invertedAbstract(inv map[string][]int) string {
	n := 0
	for _, ps := range inv {
		for _, p := range ps {
			n = max(n, p+1)
		}
	}
	words := make([]string, n)
	for w, ps := range inv {
		for _, p := range ps {
			words[p] = w
		}
	}
	return strings.Join(strings.Fields(strings.Join(words, " ")), " ")
}

func (o OpenAlex) Search(ctx context.Context, q string) ([]Hit, error) {
	var out struct {
		Results []oaWork `json:"results"`
	}
	if err := getJSON(ctx, o.HTTP, o.Base+"/works?per-page=10&search="+url.QueryEscape(q), &out); err != nil {
		return nil, err
	}
	hits := make([]Hit, len(out.Results))
	for i, w := range out.Results {
		hits[i] = w.hit()
	}
	return hits, nil
}

// Get returns the abstract: OpenAlex holds no full text.
func (o OpenAlex) Get(ctx context.Context, id string) (Doc, error) {
	var w oaWork
	if err := getJSON(ctx, o.HTTP, o.Base+"/works/"+url.PathEscape(id), &w); err != nil {
		return Doc{}, err
	}
	h := w.hit()
	return Doc{Hit: h, Text: h.Abstract}, nil
}

type EuropePMC struct {
	Base string
	HTTP *http.Client
}

type epmcResult struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	PMCID       string `json:"pmcid"`
	DOI         string `json:"doi"`
	Title       string `json:"title"`
	Abstract    string `json:"abstractText"`
	PubYear     string `json:"pubYear"`
	PubTypeList struct {
		PubType []string `json:"pubType"`
	} `json:"pubTypeList"`
}

func (r epmcResult) hit() Hit {
	id := "europepmc:" + r.Source + ":" + r.ID
	if r.PMCID != "" {
		id = "europepmc:" + r.PMCID
	}
	year, _ := strconv.Atoi(r.PubYear)
	return Hit{ID: id, Title: r.Title, DOI: r.DOI, Year: year, Abstract: r.Abstract,
		Retracted: slices.Contains(r.PubTypeList.PubType, "Retracted Publication")}
}

func (e EuropePMC) Search(ctx context.Context, q string) ([]Hit, error) {
	var out struct {
		ResultList struct {
			Result []epmcResult `json:"result"`
		} `json:"resultList"`
	}
	u := e.Base + "/search?format=json&resultType=core&pageSize=10&query=" + url.QueryEscape(q)
	if err := getJSON(ctx, e.HTTP, u, &out); err != nil {
		return nil, err
	}
	hits := make([]Hit, len(out.ResultList.Result))
	for i, r := range out.ResultList.Result {
		hits[i] = r.hit()
	}
	return hits, nil
}

var tags = regexp.MustCompile(`<[^>]+>`)

// Get takes "PMC123" or "SRC:ID" and returns open full text when Europe PMC
// has it, otherwise the abstract.
func (e EuropePMC) Get(ctx context.Context, id string) (Doc, error) {
	q := "PMCID:" + id
	if !strings.HasPrefix(id, "PMC") {
		src, ext, ok := strings.Cut(id, ":")
		if !ok {
			return Doc{}, fmt.Errorf("europepmc id %q: want PMC… or SRC:ID", id)
		}
		q = "EXT_ID:" + ext + " AND SRC:" + src
	}
	hits, err := e.Search(ctx, q)
	if err != nil {
		return Doc{}, err
	}
	if len(hits) == 0 {
		return Doc{}, fmt.Errorf("europepmc: %s not found", id)
	}
	d := Doc{Hit: hits[0], Text: hits[0].Abstract}
	if strings.HasPrefix(id, "PMC") {
		if xml, err := get(ctx, e.HTTP, e.Base+"/"+id+"/fullTextXML"); err == nil {
			if txt := strings.Join(strings.Fields(html.UnescapeString(tags.ReplaceAllString(string(xml), " "))), " "); txt != "" {
				d.Text, d.FullText = txt, true
			}
		}
	}
	return d, nil
}

type GEO struct {
	Base string
	HTTP *http.Client
}

type geoSummary struct {
	Accession string `json:"accession"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	GPL       string `json:"gpl"`
	NSamples  *int   `json:"n_samples"`
	Samples   []struct {
		Accession string `json:"accession"`
		Title     string `json:"title"`
	} `json:"samples"`
}

// UnmarshalJSON accepts GEO's integer and string count representations.
// A blank count has no value, rather than meaning zero samples.
func (s *geoSummary) UnmarshalJSON(data []byte) error {
	type summary geoSummary
	var wire struct {
		*summary
		NSamples json.RawMessage `json:"n_samples"`
	}
	wire.summary = (*summary)(s)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.NSamples = nil
	if len(wire.NSamples) == 0 || string(wire.NSamples) == "null" {
		return nil
	}
	raw := string(wire.NSamples)
	if strings.HasPrefix(raw, "\"") {
		if err := json.Unmarshal(wire.NSamples, &raw); err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("n_samples: %w", err)
	}
	s.NSamples = &n
	return nil
}

func (g GEO) summaries(ctx context.Context, term string) ([]geoSummary, error) {
	var ids struct {
		R struct {
			IDs []string `json:"idlist"`
		} `json:"esearchresult"`
	}
	if err := getJSON(ctx, g.HTTP, g.Base+"/esearch.fcgi?db=gds&retmode=json&retmax=10&term="+url.QueryEscape(term), &ids); err != nil {
		return nil, err
	}
	if len(ids.R.IDs) == 0 {
		return nil, nil
	}
	var sum struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := getJSON(ctx, g.HTTP, g.Base+"/esummary.fcgi?db=gds&retmode=json&id="+strings.Join(ids.R.IDs, ","), &sum); err != nil {
		return nil, err
	}
	var out []geoSummary
	for _, id := range ids.R.IDs {
		var s geoSummary
		raw, ok := sum.Result[id]
		if !ok {
			return nil, fmt.Errorf("geo: summary for %s missing", id)
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("geo: summary for %s: %w", id, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func (g GEO) Search(ctx context.Context, q string) ([]Hit, error) {
	ss, err := g.summaries(ctx, q)
	hits := make([]Hit, len(ss))
	for i, s := range ss {
		hits[i] = Hit{ID: s.Accession, Title: s.Title}
		if s.NSamples != nil {
			hits[i].Abstract = fmt.Sprintf("%d samples", *s.NSamples)
		}
	}
	return hits, err
}

// Open returns the dataset's structure as text: what each sample is.
func (g GEO) Open(ctx context.Context, accession string) (string, error) {
	ss, err := g.summaries(ctx, accession+"[ACCN]")
	if err != nil {
		return "", err
	}
	for _, s := range ss {
		if s.Accession != accession {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "accession: %s\ntitle: %s\nplatform: GPL%s\nsummary: %s\n", s.Accession, s.Title, s.GPL, s.Summary)
		if s.NSamples == nil {
			b.WriteString("samples:\n")
		} else {
			fmt.Fprintf(&b, "samples (%d):\n", *s.NSamples)
		}
		for _, sm := range s.Samples {
			fmt.Fprintf(&b, "- %s: %s\n", sm.Accession, sm.Title)
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("geo: %s not found", accession)
}
