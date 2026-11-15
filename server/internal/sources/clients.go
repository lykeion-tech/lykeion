// Package sources talks to literature and dataset APIs and serves them to
// roles as MCP tools that record every call on the card.
package sources

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	OpenAlexBase  = "https://api.openalex.org"
	EuropePMCBase = "https://www.ebi.ac.uk/europepmc/webservices/rest"
	EUtilsBase    = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"
	ArxivBase     = "https://export.arxiv.org/api"
)

type Hit struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	DOI       string `json:"doi,omitempty"`
	Year      int    `json:"year,omitempty"`
	Abstract  string `json:"abstract,omitempty"`
	Retracted bool   `json:"retracted,omitempty"`
	PMID      string `json:"pmid,omitempty"`
}

// ErrNotFound marks an answer that the record does not exist, as opposed to
// an upstream that could not answer.
var ErrNotFound = errors.New("not found")

// statusError is an upstream's non-200 answer; a 404 or 410 is ErrNotFound.
type statusError struct {
	msg  string
	code int
}

func (e statusError) Error() string { return e.msg }

func (e statusError) Is(target error) bool {
	return target == ErrNotFound && (e.code == http.StatusNotFound || e.code == http.StatusGone)
}

type Doc struct {
	Hit
	Text       string
	FullText   bool
	SourceKind string
	SourceURL  string
	Truncated  bool
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
	for attempt := 0; ; attempt++ {
		resp, err := h.Do(req)
		if err != nil {
			if ue := (*url.Error)(nil); errors.As(err, &ue) {
				ue.URL = shown(req.URL)
			}
			return nil, err
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return b, err
		}
		retryAfter := resp.Header.Get("Retry-After")
		msg := fmt.Sprintf("GET %s: %s", shown(req.URL), resp.Status)
		if retryAfter != "" {
			msg += "; Retry-After: " + retryAfter
		}
		failure := statusError{msg, resp.StatusCode}
		if attempt == 2 || (resp.StatusCode != http.StatusTooManyRequests && (resp.StatusCode < 500 || resp.StatusCode >= 600)) {
			return nil, failure
		}
		if err := waitRetry(ctx, attempt, retryAfter); err != nil {
			return nil, fmt.Errorf("%v: %w", failure, err)
		}
	}
}

func waitRetry(ctx context.Context, attempt int, retryAfter string) error {
	delay := time.Duration(float64(time.Duration(1<<attempt)*500*time.Millisecond) * (1 - rand.Float64()/4))
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 && seconds <= int((1<<63-1)/int64(time.Second)) {
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(retryAfter); err == nil {
		delay = max(time.Until(when), 0)
	}
	if deadline, ok := ctx.Deadline(); ok && delay >= time.Until(deadline) {
		return context.DeadlineExceeded
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// shown is u as an error may name it: errors reach agents, so an upstream
// key is cut out.
func shown(u *url.URL) string {
	c := *u
	q := c.Query()
	if !q.Has("api_key") {
		return u.String()
	}
	q.Del("api_key")
	c.RawQuery = q.Encode()
	return c.String()
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
	// Mailto is the server's contact email, sent so OpenAlex serves its
	// requests from the polite pool; empty sends none.
	Mailto string
	// Key is the server's OpenAlex API key, which raises its daily budget
	// tenfold over keyless calls; empty sends none.
	Key string
}

// contact is the mailto and api_key parameters to append to a request, or "".
func (o OpenAlex) contact(sep string) string {
	q := url.Values{}
	if o.Mailto != "" {
		q.Set("mailto", o.Mailto)
	}
	if o.Key != "" {
		q.Set("api_key", o.Key)
	}
	if len(q) == 0 {
		return ""
	}
	return sep + q.Encode()
}

type oaWork struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	DOI       string           `json:"doi"`
	Year      int              `json:"publication_year"`
	Retracted bool             `json:"is_retracted"`
	Inv       map[string][]int `json:"abstract_inverted_index"`
	IDs       struct {
		PMID string `json:"pmid"`
	} `json:"ids"`
}

func (w oaWork) hit() Hit {
	return Hit{ID: "openalex:" + path.Base(w.ID), Title: w.Title, DOI: strings.TrimPrefix(w.DOI, "https://doi.org/"),
		Year: w.Year, Abstract: invertedAbstract(w.Inv), Retracted: w.Retracted, PMID: strings.TrimPrefix(w.IDs.PMID, "https://pubmed.ncbi.nlm.nih.gov/")}
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
	if err := getJSON(ctx, o.HTTP, o.Base+"/works?per-page=10&search="+url.QueryEscape(q)+o.contact("&"), &out); err != nil {
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
	if err := getJSON(ctx, o.HTTP, o.Base+"/works/"+url.PathEscape(id)+o.contact("?"), &w); err != nil {
		return Doc{}, err
	}
	h := w.hit()
	return Doc{Hit: h, Text: h.Abstract}, nil
}

// Arxiv searches arXiv's own API. arXiv asks callers to leave a few seconds
// between requests, so every call through one Arxiv waits its turn.
type Arxiv struct {
	Base string
	HTTP *http.Client
	// Gap is the least time between two calls; zero uses ArxivGap.
	Gap time.Duration

	mu   sync.Mutex
	last time.Time
}

const ArxivGap = 3 * time.Second

var (
	arxivWord = regexp.MustCompile(`[\pL\pN]+`)
	// arxivRef is a new-style (2106.01234) or old-style (hep-th/9901001) id.
	arxivRef = regexp.MustCompile(`^(\d{4}\.\d{4,5}|[a-z-]+(\.[A-Z]{2})?/\d{7})$`)
	arxivVer = regexp.MustCompile(`v\d+$`)
	// arxivStop are the words arXiv's index leaves out: required, they match
	// nothing.
	arxivStop = strings.Fields("a an and are as at be but by for if in into is it no not of on or such that the their then there these they this to was will with")
)

type axEntry struct {
	ID        string `xml:"id"`
	Published string `xml:"published"`
	Title     string `xml:"title"`
	Summary   string `xml:"summary"`
	DOI       string `xml:"http://arxiv.org/schemas/atom doi"`
}

func (e axEntry) hit() Hit {
	id := arxivVer.ReplaceAllString(strings.TrimPrefix(strings.TrimPrefix(e.ID, "http://arxiv.org/abs/"), "https://arxiv.org/abs/"), "")
	doi := e.DOI
	if doi == "" {
		doi = "10.48550/arXiv." + id
	}
	year, _ := strconv.Atoi(e.Published[:min(4, len(e.Published))])
	return Hit{ID: "arxiv:" + id, Title: strings.Join(strings.Fields(e.Title), " "), DOI: doi, Year: year,
		Abstract: strings.Join(strings.Fields(e.Summary), " ")}
}

// wait holds the call until Gap has passed since the one before.
func (a *Arxiv) wait(ctx context.Context) error {
	gap := a.Gap
	if gap == 0 {
		gap = ArxivGap
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := time.Until(a.last.Add(gap)); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	a.last = time.Now()
	return nil
}

func (a *Arxiv) query(ctx context.Context, q url.Values) ([]Hit, error) {
	if err := a.wait(ctx); err != nil {
		return nil, err
	}
	b, err := get(ctx, a.HTTP, a.Base+"/query?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var feed struct {
		Entries []axEntry `xml:"entry"`
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		return nil, err
	}
	hits := make([]Hit, len(feed.Entries))
	for i, e := range feed.Entries {
		hits[i] = e.hit()
	}
	return hits, nil
}

// Search asks for papers holding every word of q, most relevant first.
func (a *Arxiv) Search(ctx context.Context, q string) ([]Hit, error) {
	words := slices.DeleteFunc(arxivWord.FindAllString(q, -1), func(w string) bool {
		return slices.Contains(arxivStop, strings.ToLower(w))
	})
	if len(words) == 0 {
		return nil, fmt.Errorf("arxiv: no search terms in %q", q)
	}
	for i, w := range words {
		words[i] = "all:" + w
	}
	return a.query(ctx, url.Values{"search_query": {strings.Join(words, " AND ")}, "sortBy": {"relevance"}, "max_results": {"10"}})
}

// Get returns the abstract: arXiv's API holds no full text.
func (a *Arxiv) Get(ctx context.Context, id string) (Doc, error) {
	id = arxivVer.ReplaceAllString(id, "")
	if !arxivRef.MatchString(id) {
		return Doc{}, fmt.Errorf("arxiv id %q: want 2106.01234 or hep-th/9901001: %w", id, ErrNotFound)
	}
	hits, err := a.query(ctx, url.Values{"id_list": {id}})
	if err != nil {
		return Doc{}, err
	}
	if len(hits) == 0 || hits[0].Title == "" {
		return Doc{}, fmt.Errorf("arxiv: %s %w", id, ErrNotFound)
	}
	return Doc{Hit: hits[0], Text: hits[0].Abstract}, nil
}

type EuropePMC struct {
	Base string
	HTTP *http.Client
}

type epmcResult struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	PMCID       string `json:"pmcid"`
	PMID        string `json:"pmid"`
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
	return Hit{ID: id, Title: r.Title, DOI: r.DOI, Year: year, Abstract: r.Abstract, PMID: r.PMID,
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
		return Doc{}, fmt.Errorf("europepmc: %s %w", id, ErrNotFound)
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
	MetadataBase string
	Base         string
	HTTP         *http.Client
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
		var entity map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entity); err != nil || len(entity) == 0 {
			return nil, fmt.Errorf("geo: summary for %s missing or empty", id)
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
	d, err := g.OpenWithMetadata(ctx, accession)
	return d.Structure, err
}

func (g GEO) OpenWithMetadata(ctx context.Context, accession string) (DatasetDocument, error) {
	ss, err := g.summaries(ctx, accession+"[ACCN]")
	if err != nil {
		return DatasetDocument{}, err
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
		var refs []MetadataRef
		if geoAccession.MatchString(accession) {
			refs = append(refs, MetadataRef{SourceID: "geo:" + accession, Accession: accession, Relation: "self"})
		}
		seen := map[string]bool{accession: true}
		for _, sm := range s.Samples {
			if geoAccession.MatchString(sm.Accession) && strings.HasPrefix(sm.Accession, "GSM") && !seen[sm.Accession] {
				refs = append(refs, MetadataRef{SourceID: "geo:" + sm.Accession, Accession: sm.Accession, Relation: "sample"})
				seen[sm.Accession] = true
			}
		}
		return DatasetDocument{Structure: b.String(), MetadataSources: refs}, nil
	}
	return DatasetDocument{}, fmt.Errorf("geo: %s %w", accession, ErrNotFound)
}
