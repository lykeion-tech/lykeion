package sources

import (
	"context"
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
				fmt.Fprint(w, `{"id":"https://openalex.org/W1","title":"OA paper","doi":"https://doi.org/10.1/x","is_retracted":true}`)
			case "arxiv":
				fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>http://arxiv.org/abs/2106.01234v2</id><published>2021-06-01</published><title>AX paper</title><summary>abs</summary></entry></feed>`)
			case "epmc":
				fmt.Fprint(w, `{"resultList":{"result":[{"id":"12345","source":"MED","title":"EPMC paper","doi":"10.2/y"}]}}`)
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
	if err != nil || h.Title != "OA paper" || !h.Retracted {
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
	if err != nil || h.Title != "EPMC paper" {
		t.Fatalf("pmid: %+v %v", h, err)
	}
	if want := "epmc /search?format=json&resultType=core&pageSize=10&query=EXT_ID%3A12345+AND+SRC%3AMED"; seen[len(seen)-1] != want {
		t.Errorf("pmid reached %q", seen[len(seen)-1])
	}
	n := len(seen)
	for _, bad := range []string{"isbn:123", "10.1/x", ""} {
		if _, err := l.Paper(ctx, bad); err == nil {
			t.Errorf("%q: no error", bad)
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
	if _, err := l.Dataset(ctx, "GSE2"); err == nil {
		t.Error("GSE2 not in GEO: no error")
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
	if err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("a loopback link: %v", err)
	}
}
