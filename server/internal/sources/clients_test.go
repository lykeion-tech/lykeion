package sources

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestUpstreamCallsAreBounded pins the default client's bounds, then shows
// each bound cuts a stalled upstream.
func TestUpstreamCallsAreBounded(t *testing.T) {
	if client.Transport == nil {
		t.Fatal("default client has no bounded transport")
	}
	if client.Timeout != 30*time.Second || client.Transport.(*http.Transport).TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("default client: call %v, connect %v", client.Timeout, client.Transport.(*http.Transport).TLSHandshakeTimeout)
	}
	ctx := context.Background()

	// An upstream that accepts the request and never answers.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hang.Close()
	start := time.Now()
	if _, err := get(ctx, newClient(200*time.Millisecond, 5*time.Second), hang.URL); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("a silent upstream: %v after %v", err, time.Since(start))
	}

	// An upstream that accepts the connection and never completes TLS.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 8)
	done := make(chan struct{})
	defer func() {
		ln.Close()
		<-done
		close(held)
		for c := range held {
			c.Close()
		}
	}()
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c
		}
	}()
	start = time.Now()
	if _, err := get(ctx, newClient(5*time.Second, 200*time.Millisecond), "https://"+ln.Addr().String()); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("a stalled handshake: %v after %v", err, time.Since(start))
	}
}

func TestOpenAlexNamesItsContact(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("mailto"))
		if strings.HasSuffix(r.URL.Path, "/works") {
			w.Write([]byte(`{"results":[]}`))
			return
		}
		w.Write([]byte(`{"id":"https://openalex.org/W1","title":"t"}`))
	}))
	defer srv.Close()
	oa := OpenAlex{Base: srv.URL, Mailto: "lab@example.org"}
	if _, err := oa.Search(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := oa.Get(context.Background(), "W1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "lab@example.org" || got[1] != "lab@example.org" {
		t.Fatalf("mailto sent: %q", got)
	}
	got = nil
	if _, err := (OpenAlex{Base: srv.URL}).Search(context.Background(), "q"); err != nil || got[0] != "" {
		t.Fatalf("no contact, yet mailto %q (%v)", got, err)
	}
}

func TestOpenAlexSendsItsKeyAndNeverShowsIt(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("api_key"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/W9"):
			http.Error(w, "budget spent", http.StatusTooManyRequests)
		case strings.HasSuffix(r.URL.Path, "/works"):
			w.Write([]byte(`{"results":[]}`))
		default:
			w.Write([]byte(`{"id":"https://openalex.org/W1","title":"t"}`))
		}
	}))
	defer srv.Close()
	oa := OpenAlex{Base: srv.URL, Mailto: "lab@example.org", Key: "sekret-key"}
	if _, err := oa.Search(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := oa.Get(context.Background(), "W1"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "sekret-key" || got[1] != "sekret-key" {
		t.Fatalf("api_key sent: %q", got)
	}
	_, err := oa.Get(context.Background(), "W9")
	if err == nil || strings.Contains(err.Error(), "sekret-key") || !strings.Contains(err.Error(), "429") {
		t.Fatalf("a failed call's error must name the status, never the key: %v", err)
	}
	got = nil
	if _, err := (OpenAlex{Base: srv.URL}).Search(context.Background(), "q"); err != nil || got[0] != "" {
		t.Fatalf("no key, yet api_key %q (%v)", got, err)
	}
}

const arxivFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <id>http://arxiv.org/abs/1706.03762v7</id>
    <published>2017-06-12T17:57:34Z</published>
    <title>Attention Is All
      You Need</title>
    <summary>  The dominant sequence transduction models
      are based on recurrent networks. </summary>
    <arxiv:doi>10.65215/journal.1</arxiv:doi>
  </entry>
  <entry>
    <id>http://arxiv.org/abs/hep-th/9901001v1</id>
    <published>1999-01-01T00:00:00Z</published>
    <title>Old style</title>
    <summary>s</summary>
  </entry>
</feed>`

func TestArxivSearchAndGetReturnArxivIDs(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Write([]byte(arxivFeed))
	}))
	defer srv.Close()
	ax := &Arxiv{Base: srv.URL, Gap: time.Millisecond}
	hits, err := ax.Search(context.Background(), `The attention is "all you need"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits: %+v", hits)
	}
	h := hits[0]
	if h.ID != "arxiv:1706.03762" || h.Title != "Attention Is All You Need" || h.Year != 2017 ||
		h.DOI != "10.65215/journal.1" || h.Abstract != "The dominant sequence transduction models are based on recurrent networks." {
		t.Errorf("hit: %+v", h)
	}
	if hits[1].ID != "arxiv:hep-th/9901001" || hits[1].DOI != "10.48550/arXiv.hep-th/9901001" {
		t.Errorf("old-style id or arXiv DOI: %+v", hits[1])
	}
	q, _ := url.ParseQuery(queries[0])
	if q.Get("search_query") != "all:attention AND all:all AND all:you AND all:need" || q.Get("max_results") != "10" {
		t.Errorf("search query: %q", queries[0])
	}
	d, err := ax.Get(context.Background(), "1706.03762")
	if err != nil {
		t.Fatal(err)
	}
	q, _ = url.ParseQuery(queries[1])
	if q.Get("id_list") != "1706.03762" || d.ID != "arxiv:1706.03762" || d.Text != h.Abstract || d.FullText {
		t.Errorf("get: %q %+v", queries[1], d)
	}
	if _, err := ax.Get(context.Background(), "x y"); err == nil {
		t.Error("an id that is not arXiv's was fetched")
	}
}

// TestArxivPacesItsCalls: arXiv asks for one request every few seconds, so
// calls from every run share one gap.
func TestArxivPacesItsCalls(t *testing.T) {
	var at []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at = append(at, time.Now())
		w.Write([]byte(arxivFeed))
	}))
	defer srv.Close()
	ax := &Arxiv{Base: srv.URL, Gap: 150 * time.Millisecond}
	for range 3 {
		if _, err := ax.Search(context.Background(), "q"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(at); i++ {
		if d := at[i].Sub(at[i-1]); d < 140*time.Millisecond {
			t.Fatalf("call %d came %v after the one before", i, d)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := ax.Search(ctx, "q"); err == nil {
		t.Error("a call whose context ended while waiting its turn went ahead")
	}
}
