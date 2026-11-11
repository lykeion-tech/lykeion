package sources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLookupPaperIDs(t *testing.T) {
	var seen []string
	note := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, name+" "+r.URL.RequestURI())
			switch name {
			case "openalex":
				fmt.Fprint(w, `{"id":"https://openalex.org/W1","title":"OA paper","doi":"https://doi.org/10.1/x","is_retracted":true,"ids":{"pmid":"https://pubmed.ncbi.nlm.nih.gov/777"}}`)
			case "arxiv":
				fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>http://arxiv.org/abs/2106.01234v2</id><published>2021-06-01</published><title>AX paper</title><summary>abs</summary></entry></feed>`)
			case "epmc":
				fmt.Fprint(w, `{"resultList":{"result":[{"id":"12345","source":"MED","title":"EPMC paper","doi":"10.2/y","pmid":"12345","abstractText":"abs"}]}}`)
			}
		}
	}
	oa := httptest.NewServer(note("openalex"))
	defer oa.Close()
	ax := httptest.NewServer(note("arxiv"))
	defer ax.Close()
	ep := httptest.NewServer(note("epmc"))
	defer ep.Close()
	l := Lookup{
		OpenAlex:  OpenAlex{Base: oa.URL, HTTP: oa.Client()},
		Arxiv:     &Arxiv{Base: ax.URL, HTTP: ax.Client(), Gap: time.Nanosecond},
		EuropePMC: EuropePMC{Base: ep.URL, HTTP: ep.Client()},
	}
	ctx := context.Background()

	h, err := l.Paper(ctx, "doi:10.1/x")
	if err != nil || h.Title != "OA paper" || !h.Retracted || h.PMID != "777" {
		t.Fatalf("doi: %+v %v", h, err)
	}
	if seen[len(seen)-1] != "openalex /works/doi:10.1%2Fx" {
		t.Errorf("doi reached %q", seen[len(seen)-1])
	}
	h, err = l.Paper(ctx, "arxiv:2106.01234")
	if err != nil || h.ID != "arxiv:2106.01234" {
		t.Fatalf("arxiv: %+v %v", h, err)
	}
	if seen[len(seen)-1] != "arxiv /query?id_list=2106.01234" {
		t.Errorf("arxiv reached %q", seen[len(seen)-1])
	}
	h, err = l.Paper(ctx, "pmid:12345")
	if err != nil || h.Title != "EPMC paper" || h.PMID != "12345" {
		t.Fatalf("pmid: %+v %v", h, err)
	}
	if want := "epmc /search?format=json&resultType=core&pageSize=10&query=EXT_ID%3A12345+AND+SRC%3AMED"; seen[len(seen)-1] != want {
		t.Errorf("pmid reached %q", seen[len(seen)-1])
	}
	n := len(seen)
	for _, bad := range []string{"isbn:123", "10.1/x", ""} {
		if _, err := l.Paper(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v, want not found", bad, err)
		}
	}
	if len(seen) != n {
		t.Errorf("a bad id reached an upstream: %v", seen[n:])
	}
}

func TestLookupDataset(t *testing.T) {
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/esearch.fcgi":
			fmt.Fprint(w, `{"esearchresult":{"idlist":["1"]}}`)
		case "/esummary.fcgi":
			fmt.Fprint(w, `{"result":{"1":{"accession":"GSE1","title":"Mouse liver","summary":"s","gpl":"1"}}}`)
		}
	}))
	defer geo.Close()
	l := Lookup{GEO: GEO{Base: geo.URL, HTTP: geo.Client()}}
	ctx := context.Background()
	if h, err := l.Dataset(ctx, "GSE1"); err != nil || h.ID != "GSE1" || h.Abstract == "" {
		t.Fatalf("GSE1: %+v %v", h, err)
	}
	if _, err := l.Dataset(ctx, "GSE2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GSE2 not in GEO: %v", err)
	}

	var method string
	link := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		if r.URL.Path != "/ok" {
			http.NotFound(w, r)
		}
	}))
	defer link.Close()
	defer func(c *http.Client) { linkClient = c }(linkClient)
	linkClient = link.Client()
	if h, err := l.Dataset(ctx, link.URL+"/ok"); err != nil || h.ID != link.URL+"/ok" || method != "HEAD" {
		t.Fatalf("link: %+v %v %s", h, err, method)
	}
	if _, err := l.Dataset(ctx, link.URL+"/gone"); err == nil {
		t.Error("a 404 link: no error")
	}
	if _, err := l.Dataset(ctx, "http"+link.URL[len("https"):]+"/ok"); err == nil {
		t.Error("a plain http link: no error")
	}
}

func TestLookupLinkRefusesPrivateAddresses(t *testing.T) {
	link := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer link.Close()
	_, err := (Lookup{}).Dataset(context.Background(), link.URL)
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("a loopback link: %v", err)
	}
}

func TestLookupNotFoundIsNotAnOutage(t *testing.T) {
	code := http.StatusNotFound
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" && code == http.StatusOK {
			fmt.Fprint(w, `{"resultList":{"result":[]}}`)
			return
		}
		w.WriteHeader(code)
	}))
	defer up.Close()
	l := Lookup{OpenAlex: OpenAlex{Base: up.URL, HTTP: up.Client()}, EuropePMC: EuropePMC{Base: up.URL, HTTP: up.Client()},
		Arxiv: &Arxiv{Base: up.URL, HTTP: up.Client(), Gap: time.Nanosecond}}
	ctx := context.Background()
	if _, err := l.Paper(ctx, "doi:10.1/none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenAlex 404: %v", err)
	}
	code = http.StatusOK
	if _, err := l.Paper(ctx, "pmid:1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Europe PMC with no result: %v", err)
	}
	code = http.StatusServiceUnavailable
	for _, id := range []string{"doi:10.1/x", "pmid:1", "arxiv:2106.01234"} {
		if _, err := l.Paper(ctx, id); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a 503: %v, want an outage", id, err)
		}
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	if _, err := (Lookup{OpenAlex: OpenAlex{Base: dead.URL}}).Paper(ctx, "doi:10.1/x"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("a closed upstream: %v, want an outage", err)
	}

	link := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	defer link.Close()
	defer func(c *http.Client) { linkClient = c }(linkClient)
	linkClient = link.Client()
	code = http.StatusNotFound
	if _, err := l.Dataset(ctx, link.URL); !errors.Is(err, ErrNotFound) {
		t.Errorf("a 404 link: %v", err)
	}
}

// A link is the agent's choice, so a link that does not answer fails its
// item; only Lykeion's own upstreams have outages.
func TestLookupBrokenLinkIsNotFound(t *testing.T) {
	ctx := context.Background()
	var methods []string
	head := http.StatusOK
	link := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch {
		case r.URL.Path == "/down":
			w.WriteHeader(http.StatusBadGateway)
		case r.Method == http.MethodHead:
			w.WriteHeader(head)
		default:
			fmt.Fprint(w, strings.Repeat("x", 1<<20))
		}
	}))
	defer link.Close()
	defer func(c *http.Client) { linkClient = c }(linkClient)
	linkClient = link.Client()
	if _, err := (Lookup{}).Dataset(ctx, link.URL+"/down"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "502") {
		t.Errorf("a 502 link: %v, want not found with the status", err)
	}
	for _, code := range []int{http.StatusMethodNotAllowed, http.StatusNotImplemented} {
		head, methods = code, nil
		if h, err := (Lookup{}).Dataset(ctx, link.URL+"/data"); err != nil || h.ID != link.URL+"/data" || strings.Join(methods, " ") != "HEAD GET" {
			t.Errorf("HEAD %d then GET 200: %+v %v %v", code, h, err, methods)
		}
	}
	dead := httptest.NewTLSServer(http.NotFoundHandler())
	dead.Close()
	if _, err := (Lookup{}).Dataset(ctx, dead.URL); !errors.Is(err, ErrNotFound) {
		t.Errorf("a link nothing answers: %v, want not found", err)
	}
}

func TestLookupKeepsExcludedPapersBack(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/works":
			fmt.Fprint(w, `{"results":[{"id":"https://openalex.org/W1","title":"Hidden","doi":"https://doi.org/10.1/hidden"},{"id":"https://openalex.org/W2","title":"Open","doi":"https://doi.org/10.1/open"}]}`)
		case strings.HasPrefix(r.URL.Path, "/works/"):
			fmt.Fprint(w, `{"id":"https://openalex.org/W1","title":"Hidden","doi":"https://doi.org/10.1/hidden"}`)
		default:
			fmt.Fprint(w, `{"resultList":{"result":[{"id":"9","source":"MED","title":"By PMID","doi":"10.1/hidden","pmid":"9"}]}}`)
		}
	}))
	defer up.Close()
	l := Lookup{OpenAlex: OpenAlex{Base: up.URL, HTTP: up.Client()}, EuropePMC: EuropePMC{Base: up.URL, HTTP: up.Client()},
		Exclude: excluded(t, "10.1/hidden")}
	ctx := context.Background()
	hits, err := l.Search(ctx, "q")
	if err != nil || len(hits) != 1 || hits[0].DOI != "10.1/open" {
		t.Fatalf("search: %+v %v", hits, err)
	}
	for _, id := range []string{"doi:10.1/hidden", "pmid:9"} {
		if h, err := l.Paper(ctx, id); !errors.Is(err, ErrNotFound) || h.ID != "" {
			t.Errorf("%s, excluded: %+v %v, want not found", id, h, err)
		}
	}
}

// Europe PMC often holds no abstract for an older paper that OpenAlex has by
// its DOI; a PMID lookup takes OpenAlex's abstract and keeps the rest.
func TestLookupPaperPMIDTakesAbstractFromDOI(t *testing.T) {
	var oaCalls []string
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oaCalls = append(oaCalls, r.URL.RequestURI())
		fmt.Fprint(w, `{"id":"https://openalex.org/W9","title":"Dentinogenesis in the incisors of scorbutic guinea pigs","doi":"https://doi.org/10.2/y","abstract_inverted_index":{"Teeth":[0],"grow.":[1]}}`)
	}))
	defer oa.Close()
	abstract := ""
	ep := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"resultList":{"result":[{"id":"12345","source":"MED","title":"EPMC paper","doi":"10.2/y","pmid":"12345","pubYear":"1966","abstractText":%q}]}}`, abstract)
	}))
	defer ep.Close()
	l := Lookup{OpenAlex: OpenAlex{Base: oa.URL, HTTP: oa.Client()}, EuropePMC: EuropePMC{Base: ep.URL, HTTP: ep.Client()}}
	h, err := l.Paper(t.Context(), "pmid:12345")
	if err != nil || h.Abstract != "Teeth grow." || h.ID != "europepmc:MED:12345" || h.Title != "EPMC paper" || h.DOI != "10.2/y" || h.PMID != "12345" || h.Year != 1966 {
		t.Fatalf("no abstract: %+v %v", h, err)
	}
	if len(oaCalls) != 1 || oaCalls[0] != "/works/doi:10.2%2Fy" {
		t.Fatalf("OpenAlex asked %v", oaCalls)
	}

	abstract = "Europe PMC's own abstract."
	oaCalls = nil
	h, err = l.Paper(t.Context(), "pmid:12345")
	if err != nil || h.Abstract != abstract || len(oaCalls) != 0 {
		t.Fatalf("with an abstract: %+v %v; OpenAlex asked %v", h, err, oaCalls)
	}

	// The OpenAlex record is the same paper, so its exclusion holds too.
	abstract = ""
	l.Exclude = excluded(t, "Dentinogenesis in the incisors of scorbutic guinea pigs")
	if _, err = l.Paper(t.Context(), "pmid:12345"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excluded paper: %v", err)
	}
}
