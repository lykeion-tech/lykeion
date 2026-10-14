package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGEOSampleCountWireRepresentations(t *testing.T) {
	for _, count := range []string{`"2"`, `2`, `""`} {
		t.Run(count, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/esearch.fcgi":
					fmt.Fprint(w, `{"esearchresult":{"idlist":["200001"]}}`)
				case "/esummary.fcgi":
					fmt.Fprintf(w, `{"result":{"uids":["200001"],"200001":{"accession":"GSE1","title":"Mouse liver","summary":"Dataset summary","gpl":"1234","n_samples":%s,"samples":[{"accession":"GSM1","title":"mouse 1"},{"accession":"GSM2","title":"mouse 2"}]}}}`, count)
				default:
					t.Errorf("unexpected upstream path %s", r.URL.Path)
				}
			}))
			defer api.Close()
			g := GEO{Base: api.URL, HTTP: api.Client()}
			hits, err := g.Search(context.Background(), "mouse liver")
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(hits) != 1 || hits[0].ID != "GSE1" || hits[0].Abstract != map[bool]string{true: "", false: "2 samples"}[count == `""`] {
				t.Fatalf("Search hits: %+v", hits)
			}
			text, err := g.Open(context.Background(), "GSE1")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if count == `""` {
				if strings.Contains(text, "samples (0)") || strings.Contains(text, "samples (2)") {
					t.Errorf("absent count rendered: %s", text)
				}
			} else if !strings.Contains(text, "samples (2):\n") {
				t.Errorf("count missing: %s", text)
			}
			for _, want := range []string{"accession: GSE1\n", "- GSM1: mouse 1\n", "- GSM2: mouse 2\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("recordable text missing %q: %s", want, text)
				}
			}
		})
	}
}

func TestGEOMalformedSampleCountReturnsNoResult(t *testing.T) {
	for _, count := range []string{`"bad"`, `" "`, `1.5`, `true`, `{}`, `[]`} {
		t.Run(count, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/esearch.fcgi" {
					fmt.Fprint(w, `{"esearchresult":{"idlist":["200001"]}}`)
					return
				}
				fmt.Fprintf(w, `{"result":{"200001":{"accession":"GSE1","n_samples":%s}}}`, count)
			}))
			defer api.Close()
			g := GEO{Base: api.URL, HTTP: api.Client()}
			if hits, err := g.Search(context.Background(), "mouse"); err == nil || len(hits) != 0 {
				t.Fatalf("Search returned %+v, %v", hits, err)
			}
			if text, err := g.Open(context.Background(), "GSE1"); err == nil || text != "" {
				t.Fatalf("Open returned %q, %v", text, err)
			}

		})
	}
}

// The summary fields reproduce the mixed entity shapes in GEO's public response.
func TestGEOMixedEntitySummaries(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/esearch.fcgi" {
			fmt.Fprint(w, `{"esearchresult":{"idlist":["200157049","100024247","304751639"]}}`)
			return
		}
		fmt.Fprint(w, `{"result":{"uids":["200157049","100024247","304751639"],"200157049":{"accession":"GSE157049","title":"RNA-seq of metformin treatment in liver in WT, Raptor Ser-Ala mutant, Tsc2-null, Raptor mutant;Tsc2-null, and Ampk-null","summary":"Despite being the frontline therapy for Type 2 diabetes, the mechanisms of action of the biguanide drug metformin are still being discovered. In particular, the detailed molecular interplays between the AMPK and the mTORC1 pathway in the hepatic benefits of metformin are still ill-defined. Metformin-dependent activation of AMPK classically inhibits mTORC1 via TSC/RHEB. But several lines of evidence suggest additional mechanisms at play in metformin inhibition of mTORC1. Here we investigated the role of direct AMPK-mediated serine phosphorylation of RAPTOR in a new RaptorAA mouse model, in which AMPK phospho-serine sites Ser722 and Ser792 of RAPTOR were mutated to alanine. Metformin treatment of intact murine liver requires AMPK regulation of both RAPTOR and TSC2 to fully inhibit mTORC1, and this regulation is critical for the transcriptional response to metformin. Transcriptionally, AMPK and mTORC1 were both important for regulation of anabolic metabolism and inflammatory programs triggered by metformin treatment. The hepatic transcriptional response in mice on high fat diet treated with metformin was largely ablated by AMPK-deficiency under the conditions examined, indicating the essential role of this kinase and its targets in metformin action in vivo.","gpl":"24247","entrytype":"GSE","samples":[{"accession":"GSM4751620","title":"liver_AA Met rep1"},{"accession":"GSM4751614","title":"liver_WT Met rep1"},{"accession":"GSM4751634","title":"liver_Adel Sal rep1"},{"accession":"GSM4751617","title":"liver_AA Sal rep1"},{"accession":"GSM4751637","title":"liver_Adel Met rep1"},{"accession":"GSM4751623","title":"liver_Tsc Sal rep1"},{"accession":"GSM4751626","title":"liver_Tsc Met rep2"},{"accession":"GSM4751639","title":"liver_Adel Met rep3"},{"accession":"GSM4751611","title":"liver_WT Sal rep1"},{"accession":"GSM4751622","title":"liver_AA Met rep3"},{"accession":"GSM4751625","title":"liver_Tsc Met rep1"},{"accession":"GSM4751631","title":"liver_AATsc Met rep1"},{"accession":"GSM4751619","title":"liver_AA Sal rep3"},{"accession":"GSM4751630","title":"liver_AATsc Sal rep3"},{"accession":"GSM4751628","title":"liver_AATsc Sal rep1"},{"accession":"GSM4751624","title":"liver_Tsc Sal rep2"},{"accession":"GSM4751627","title":"liver_Tsc Met rep3"},{"accession":"GSM4751613","title":"liver_WT Sal rep3"},{"accession":"GSM4751633","title":"liver_AATsc Met rep3"},{"accession":"GSM4751636","title":"liver_Adel Sal rep3"},{"accession":"GSM4751629","title":"liver_AATsc Sal rep2"},{"accession":"GSM4751612","title":"liver_WT Sal rep2"},{"accession":"GSM4751616","title":"liver_WT Met rep3"},{"accession":"GSM4751621","title":"liver_AA Met rep2"},{"accession":"GSM4751615","title":"liver_WT Met rep2"},{"accession":"GSM4751632","title":"liver_AATsc Met rep2"},{"accession":"GSM4751618","title":"liver_AA Sal rep2"},{"accession":"GSM4751635","title":"liver_Adel Sal rep2"},{"accession":"GSM4751638","title":"liver_Adel Met rep2"}],"n_samples":29},"100024247":{"accession":"GPL24247","title":"Illumina NovaSeq 6000 (Mus musculus)","summary":"","gpl":"24247","entrytype":"GPL","samples":[],"n_samples":908585},"304751639":{"accession":"GSM4751639","title":"liver_Adel Met rep3","summary":"Liver","gpl":"24247","entrytype":"GSM","samples":[],"n_samples":""}}}`)
	}))
	defer api.Close()
	g := GEO{Base: api.URL, HTTP: api.Client()}
	hits, err := g.Search(context.Background(), "GSE157049")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].Abstract != "29 samples" || hits[2].ID != "GSM4751639" || hits[2].Abstract != "" {
		t.Fatalf("mixed hits: %+v", hits)
	}
	text, err := g.Open(context.Background(), "GSE157049")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "samples (29):\n") || !strings.Contains(text, "GSM4751639") {
		t.Fatalf("series structure: %s", text)
	}
	text, err = g.Open(context.Background(), "GSM4751639")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "samples:\n") || strings.Contains(text, "samples (0)") {
		t.Fatalf("sample absent count: %s", text)
	}
}
