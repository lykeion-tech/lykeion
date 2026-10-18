package connectors

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func entry(t *testing.T, name string) Entry {
	t.Helper()
	e, ok := Get(name)
	if !ok {
		t.Fatalf("no connector %s", name)
	}
	return e
}

// stub counts the requests that reached the upstream.
func stub(t *testing.T, h http.HandlerFunc) (*Client, *atomic.Int32) {
	var hits atomic.Int32
	c := testutil.Upstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); h(w, r) }))
	return &Client{HTTP: c, Contact: "lab@example.org", Secret: bytes.Repeat([]byte{7}, 32)}, &hits
}

var ws = uuid.New()

func TestFetchStaysUnderTheConnectorsBase(t *testing.T) {
	var path string
	c, hits := stub(t, func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; w.Write([]byte("{}")) })
	chembl := entry(t, "chembl")
	for _, p := range []string{"/../ena/portal/api/search", "/molecule/%2e%2e/%2e%2e/ena", "//evil.example/x", "https://evil.example/x",
		"molecule", "/molecule?x=1", "/molecule#x", "/a\\b", "/a\x00"} {
		_, err := c.Do(context.Background(), ws, chembl, NewRequest("chembl", p, nil, nil), "")
		if !errors.Is(err, ErrRefused) {
			t.Errorf("path %q: %v", p, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("%d refused requests reached the upstream", hits.Load())
	}
	res, err := c.Do(context.Background(), ws, chembl, NewRequest("chembl", "/molecule/CHEMBL25.json", nil, nil), "")
	if err != nil || res.Status != 200 || path != "/chembl/api/data/molecule/CHEMBL25.json" {
		t.Fatalf("%v %+v %q", err, res, path)
	}
}

func TestTheServersParametersCannotBeSetByTheAgent(t *testing.T) {
	c, hits := stub(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) })
	for _, x := range []struct {
		name  string
		query map[string]string
	}{
		{"ncbi-gene", map[string]string{"API_KEY": "mine"}},
		{"omim", map[string]string{"apiKey": "mine"}},
		{"unpaywall", map[string]string{"email": "me@x.org"}},
		{"crossref", map[string]string{"mailto": "me@x.org"}},
	} {
		if _, err := c.Do(context.Background(), ws, entry(t, x.name), NewRequest(x.name, "/x", x.query, nil), ""); !errors.Is(err, ErrRefused) {
			t.Errorf("%s %v: %v", x.name, x.query, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a refused request reached the upstream")
	}
}

func TestOnlyTheEntrysMethodsAndFixedBody(t *testing.T) {
	var got struct {
		Query     string
		Variables map[string]any
	}
	c, hits := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(405)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"data":{}}`))
	})
	gnomad := entry(t, "gnomad")
	if _, err := c.Do(context.Background(), ws, gnomad, NewRequest("gnomad", "", nil, nil), ""); !errors.Is(err, ErrRefused) {
		t.Fatalf("a GET to a POST-only connector: %v", err)
	}
	if _, err := c.Do(context.Background(), ws, entry(t, "ensembl"), NewRequest("ensembl", "/x", nil, map[string]any{"a": 1}), ""); !errors.Is(err, ErrRefused) {
		t.Fatalf("a POST to a GET-only connector: %v", err)
	}
	for _, vars := range []map[string]any{{"variantId": "1-1-A-G"}, {"variantId": "1-1-A-G", "dataset": "gnomad_r4", "query": "{ x }"}} {
		if _, err := c.Do(context.Background(), ws, gnomad, NewRequest("gnomad", "", nil, vars), ""); !errors.Is(err, ErrRefused) {
			t.Errorf("variables %v: %v", vars, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a refused request reached the upstream")
	}
	evil := `1-1-A-G", dataset: gnomad_r4) { __schema { types { name } } } #`
	res, err := c.Do(context.Background(), ws, gnomad, NewRequest("gnomad", "", nil, map[string]any{"variantId": evil, "dataset": "gnomad_r4"}), "")
	if err != nil || res.Status != 200 {
		t.Fatalf("%v %+v", err, res)
	}
	var tmpl struct{ Query string }
	json.Unmarshal([]byte(gnomad.Body), &tmpl)
	if got.Query != tmpl.Query || got.Variables["variantId"] != evil || got.Variables["dataset"] != "gnomad_r4" {
		t.Fatalf("the query text changed or a variable moved: %+v", got)
	}
}

func TestRedirectsStayOnTheEntrysHosts(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/away"):
			http.Redirect(w, r, "https://evil.example/steal?api_key="+r.URL.Query().Get("api_key"), http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/move"):
			http.Redirect(w, r, "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/final", http.StatusFound)
		default:
			w.Write([]byte(r.URL.Path))
		}
	})
	gene := entry(t, "ncbi-gene")
	res, err := c.Do(context.Background(), ws, gene, NewRequest("ncbi-gene", "/move", nil, nil), "k-1234567890")
	if err != nil || string(res.Body) != "/entrez/eutils/final" {
		t.Fatalf("a redirect within the host: %v %q", err, res.Body)
	}
	_, err = c.Do(context.Background(), ws, gene, NewRequest("ncbi-gene", "/away", nil, nil), "k-1234567890")
	if !errors.Is(err, ErrRefused) || strings.Contains(err.Error(), "k-1234567890") {
		t.Fatalf("an off-host redirect: %v", err)
	}
}

func TestAPrivateAddressIsNeverDialled(t *testing.T) {
	local := Entry{Name: "local", Base: "https://localhost", Hosts: []string{"localhost"}, Methods: []string{"GET"}, Rate: 100}
	c := &Client{HTTP: Guarded()}
	_, err := c.Do(context.Background(), ws, local, NewRequest("local", "/x", nil, nil), "")
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("localhost: %v", err)
	}
	for addr, public := range map[string]bool{"8.8.8.8": true, "2606:4700::1111": true, "127.0.0.1": false, "10.1.2.3": false,
		"172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false,
		"::1": false, "fd00::1": false, "fe80::1": false, "::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false, "224.0.0.1": false} {
		if Public(netip.MustParseAddr(addr)) != public {
			t.Errorf("%s public = %v", addr, !public)
		}
	}
}

func TestBodiesAreCappedAtTwoMebibytes(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) { w.Write(bytes.Repeat([]byte("é"), MaxBody)) })
	res, err := c.Do(context.Background(), ws, entry(t, "ensembl"), NewRequest("ensembl", "/big", nil, nil), "")
	if err != nil || !res.Truncated || len(res.Body) > MaxBody || len(res.Body) < MaxBody-utf8.UTFMax || !utf8.Valid(res.Body) {
		t.Fatalf("%v truncated %v, %d bytes", err, res.Truncated, len(res.Body))
	}
	if got := cut([]byte("aé"), 2); string(got) != "a" {
		t.Fatalf("cut %q", got)
	}
}

func TestABinaryResponseIsRefused(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}) })
	if _, err := c.Do(context.Background(), ws, entry(t, "string"), NewRequest("string", "/image/network", nil, nil), ""); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not text") {
		t.Fatalf("%v", err)
	}
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

func TestADoubleGzippedBodyIsReadAsText(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz(gz([]byte(`{"ok":true}`))))
	})
	res, err := c.Do(context.Background(), ws, entry(t, "uniprot"), NewRequest("uniprot", "/x", nil, nil), "")
	if err != nil || string(res.Body) != `{"ok":true}` {
		t.Fatalf("%v %q", err, res.Body)
	}
}

func TestAGzipBombIsCappedAtTheBodyLimit(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz(gz(bytes.Repeat([]byte("a"), 8*MaxBody))))
	})
	res, err := c.Do(context.Background(), ws, entry(t, "uniprot"), NewRequest("uniprot", "/x", nil, nil), "")
	if err != nil || !res.Truncated || len(res.Body) != MaxBody {
		t.Fatalf("%v truncated %v, %d bytes", err, res.Truncated, len(res.Body))
	}
}

func TestThrottlingKeepsStatusAndRetryAfter(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "7"); w.WriteHeader(429) })
	res, err := c.Do(context.Background(), ws, entry(t, "ensembl"), NewRequest("ensembl", "/x", nil, nil), "")
	if err != nil || res.Status != 429 || res.RetryAfter != "7" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestTheRateLimitIsPerWorkspaceAndConnector(t *testing.T) {
	var c Client
	now := time.Now()
	for i := range 6 {
		d, ok := c.reserve("w1/arxiv", 1, now)
		if !ok || d != time.Duration(i)*time.Second {
			t.Fatalf("request %d waits %s (%v)", i, d, ok)
		}
	}
	if d, ok := c.reserve("w1/arxiv", 1, now); ok {
		t.Fatalf("a request %s away was taken", d)
	}
	if d, ok := c.reserve("w2/arxiv", 1, now); !ok || d != 0 {
		t.Fatal("another workspace shared the limit")
	}
	if d, ok := c.reserve("w1/arxiv", 1, now.Add(10*time.Second)); !ok || d != 0 {
		t.Fatal("the limit never recovered")
	}
}

func TestAKeyNeverComesBackFromTheUpstream(t *testing.T) {
	const key = "sk+live/123=abc"
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/drop") {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Write([]byte("header " + r.Header.Get("X-API-KEY") + " query " + r.URL.RawQuery))
	})
	for _, name := range []string{"materials-project", "ncbi-gene"} {
		res, err := c.Do(context.Background(), ws, entry(t, name), NewRequest(name, "/echo", nil, nil), key)
		if err != nil || bytes.Contains(res.Body, []byte(key)) || bytes.Contains(res.Body, []byte("sk%2Blive")) || !bytes.Contains(res.Body, []byte("[key removed]")) {
			t.Errorf("%s: %v %q", name, err, res.Body)
		}
	}
	_, err := c.Do(context.Background(), ws, entry(t, "ncbi-gene"), NewRequest("ncbi-gene", "/drop", nil, nil), key)
	if err == nil || strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "sk%2Blive") {
		t.Fatalf("the transport error: %v", err)
	}
}

func TestKeysAreSealedToTheirWorkspaceAndConnector(t *testing.T) {
	c := &Client{Secret: bytes.Repeat([]byte{7}, 32)}
	sealed, err := c.Seal(ws, "omim", "abc12345")
	if err != nil || bytes.Contains(sealed, []byte("abc12345")) {
		t.Fatal(err)
	}
	if k, err := c.Open(ws, "omim", sealed); err != nil || k != "abc12345" {
		t.Fatalf("%q %v", k, err)
	}
	if _, err := c.Open(uuid.New(), "omim", sealed); err == nil {
		t.Fatal("a key opened in another workspace")
	}
	if _, err := c.Open(ws, "drugbank", sealed); err == nil {
		t.Fatal("a key opened for another connector")
	}
	if _, err := (&Client{Secret: bytes.Repeat([]byte{8}, 32)}).Open(ws, "omim", sealed); err == nil {
		t.Fatal("a key opened under another secret")
	}
	if _, err := (&Client{}).Seal(ws, "omim", "abc12345"); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("no secret: %v", err)
	}
}

func TestTheSourceIDNamesOneExactRequest(t *testing.T) {
	a := NewRequest("ensembl", "/x", map[string]string{"a": "1", "b": "2"}, nil)
	b := NewRequest("ensembl", "/x", map[string]string{"b": "2", "a": "1"}, nil)
	c := NewRequest("ensembl", "/x", map[string]string{"a": "1"}, nil)
	if a.SourceID() != b.SourceID() || a.SourceID() == c.SourceID() || !strings.HasPrefix(a.SourceID(), "ensembl:") || len(a.SHA256()) != 64 {
		t.Fatalf("%s %s %s", a.SourceID(), b.SourceID(), c.SourceID())
	}
}
