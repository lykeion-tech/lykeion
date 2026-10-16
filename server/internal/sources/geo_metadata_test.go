package sources

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGEOMetadataProjection(t *testing.T) {
	want := "^SAMPLE = GSM12\n!Sample_title = unspecified observation\n!Sample_characteristics_ch1 = arbitrary_property: unknown\n!Sample_characteristics_ch1 = device: unit 7\n!Sample_growth_protocol_ch1 = first 20 minutes\n continued protocol text\n!Sample_supplementary_file = https://example.invalid/data.tsv\n"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("acc") != "GSM12" || r.URL.Query().Get("targ") != "self" || r.URL.Query().Get("form") != "text" || r.URL.Query().Get("view") != "full" {
			t.Errorf("query: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, want+"!sample_table_begin\nID_REF\tVALUE\n1\t42\n!sample_table_end\n")
	}))
	defer api.Close()
	d, err := (GEO{MetadataBase: api.URL, HTTP: api.Client()}).Metadata(context.Background(), "GSM12")
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != want || d.ID != "geo:GSM12" || d.SourceKind != "dataset_metadata" || d.SourceURL != api.URL+"?acc=GSM12&form=text&targ=self&view=full" || d.FullText || d.Truncated {
		t.Fatalf("document: %+v", d)
	}
}

func TestGEOMetadataBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"wrong entity", "^SAMPLE = GSM2\n!Sample_title = x\n", 200},
		{"wrong type", "^SERIES = GSM1\n!Series_title = x\n", 200},
		{"empty", "", 200}, {"challenge", "<html>Checking your browser</html>", 200},
		{"malformed", "^SAMPLE = GSM1\nnot SOFT\n", 200},
		{"incomplete header", "^SAMPLE = GSM1", 200},
		{"table only", "^SAMPLE = GSM1\n!sample_table_begin\n1\t42\n!sample_table_end\n", 200},
		{"rate limit", "", 429}, {"upstream", "", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer api.Close()
			d, err := (GEO{MetadataBase: api.URL, HTTP: api.Client()}).Metadata(context.Background(), "GSM1")
			if err == nil || d.Text != "" {
				t.Fatalf("got %+v, %v", d, err)
			}
			if tc.code == 429 && !strings.Contains(err.Error(), "17") {
				t.Fatalf("Retry-After absent: %v", err)
			}
		})
	}
	t.Run("invalid accession", func(t *testing.T) {
		calls := 0
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
		defer api.Close()
		for _, id := range []string{"GPL1", "GSM1?x=y", "gsm1", "GSE"} {
			if _, err := (GEO{MetadataBase: api.URL}).Metadata(context.Background(), id); err == nil {
				t.Fatal(id)
			}
		}
		if calls != 0 {
			t.Fatal(calls)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := (GEO{}).Metadata(ctx, "GSE1"); err == nil {
			t.Fatal("cancel accepted")
		}
	})
	t.Run("redirect", func(t *testing.T) {
		calls := 0
		dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
		defer dst.Close()
		src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, 302) }))
		defer src.Close()
		if _, err := (GEO{MetadataBase: src.URL, HTTP: src.Client()}).Metadata(context.Background(), "GSE1"); err == nil {
			t.Fatal("redirect accepted")
		}
		if calls != 0 {
			t.Fatal(calls)
		}
	})
	t.Run("capped", func(t *testing.T) {
		prefix := "^SERIES = GSE1\n!Series_title = no other fields supplied\n"
		body := prefix + "!Series_description = " + strings.Repeat("x", 2*1024*1024)
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		defer api.Close()
		d, err := (GEO{MetadataBase: api.URL}).Metadata(context.Background(), "GSE1")
		if err != nil {
			t.Fatal(err)
		}
		if !d.Truncated || d.Text != prefix {
			t.Fatalf("capped document: length=%d truncated=%v", len(d.Text), d.Truncated)
		}
	})
}

func TestGEOOpenMetadataReferences(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/esearch.fcgi" {
			fmt.Fprint(w, `{"esearchresult":{"idlist":["1"]}}`)
			return
		}
		if r.URL.Path != "/esummary.fcgi" {
			t.Errorf("unexpected fetch %s", r.URL)
			return
		}
		fmt.Fprint(w, `{"result":{"1":{"accession":"GSE1","title":"test","samples":[{"accession":"GSM2"},{"accession":"GSM1"},{"accession":"GSM2"}]}}}`)
	}))
	defer api.Close()
	g := GEO{Base: api.URL, HTTP: api.Client()}
	legacy, err := g.Open(context.Background(), "GSE1")
	if err != nil {
		t.Fatal(err)
	}
	d, err := g.OpenWithMetadata(context.Background(), "GSE1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Structure != legacy || calls != 4 {
		t.Fatalf("structure or extra requests: %+v %d", d, calls)
	}
	want := []MetadataRef{{"geo:GSE1", "GSE1", "self"}, {"geo:GSM2", "GSM2", "sample"}, {"geo:GSM1", "GSM1", "sample"}}
	if fmt.Sprint(d.MetadataSources) != fmt.Sprint(want) {
		t.Fatalf("refs %+v", d.MetadataSources)
	}
}

// Counting the body reads verifies the ceiling independently of the projection.
type geoCountingBody struct{ remaining, read int }

func (b *geoCountingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (*geoCountingBody) Close() error { return nil }

type geoRoundTrip func(*http.Request) (*http.Response, error)

func (f geoRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGEOMetadataBoundariesReadCeiling(t *testing.T) {
	body := &geoCountingBody{remaining: 3 * 1024 * 1024}
	h := &http.Client{Transport: geoRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
	})}
	if _, err := (GEO{HTTP: h}).Metadata(context.Background(), "GSE1"); err == nil {
		t.Fatal("malformed body accepted")
	}
	if body.read != 2*1024*1024+1 {
		t.Fatalf("read %d bytes", body.read)
	}
}
