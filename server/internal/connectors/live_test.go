//go:build live

package connectors

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// canned is one real request per keyless connector; the live check runs it
// against the real API.
var canned = map[string]Request{
	"ensembl":                NewRequest("ensembl", "/lookup/symbol/homo_sapiens/TP53", map[string]string{"content-type": "application/json"}, nil),
	"ucsc-genome":            NewRequest("ucsc-genome", "/getData/sequence", map[string]string{"genome": "hg38", "chrom": "chr7", "start": "117119148", "end": "117119178"}, nil),
	"ncbi-gene":              NewRequest("ncbi-gene", "/esearch.fcgi", map[string]string{"db": "gene", "term": "TP53[sym] AND human[orgn]", "retmode": "json"}, nil),
	"ncbi-taxonomy":          NewRequest("ncbi-taxonomy", "/esearch.fcgi", map[string]string{"db": "taxonomy", "term": "Homo sapiens", "retmode": "json"}, nil),
	"ena":                    NewRequest("ena", "/portal/api/search", map[string]string{"result": "study", "query": "tax_id=9606", "limit": "1", "format": "json"}, nil),
	"sra":                    NewRequest("sra", "/esearch.fcgi", map[string]string{"db": "sra", "term": "RNA-Seq[Strategy]", "retmax": "1", "retmode": "json"}, nil),
	"geo":                    NewRequest("geo", "/esearch.fcgi", map[string]string{"db": "gds", "term": "breast cancer", "retmax": "1", "retmode": "json"}, nil),
	"encode":                 NewRequest("encode", "/search/", map[string]string{"type": "Experiment", "assay_title": "TF ChIP-seq", "format": "json", "limit": "1"}, nil),
	"gtex":                   NewRequest("gtex", "/dataset/tissueSiteDetail", nil, nil),
	"gnomad":                 NewRequest("gnomad", "", nil, map[string]any{"variantId": "1-55039974-G-A", "dataset": "gnomad_r4"}),
	"dbsnp":                  NewRequest("dbsnp", "/esearch.fcgi", map[string]string{"db": "snp", "term": "rs7412", "retmode": "json"}, nil),
	"clinvar":                NewRequest("clinvar", "/esearch.fcgi", map[string]string{"db": "clinvar", "term": "BRCA1[gene]", "retmax": "1", "retmode": "json"}, nil),
	"myvariant":              NewRequest("myvariant", "/variant/chr7:g.140453136A>T", nil, nil),
	"gwas-catalog":           NewRequest("gwas-catalog", "/studies/GCST001633", nil, nil),
	"regulomedb":             NewRequest("regulomedb", "/search", map[string]string{"regions": "rs3768324", "genome": "GRCh38"}, nil),
	"jaspar":                 NewRequest("jaspar", "/matrix/MA0139.1/", map[string]string{"format": "json"}, nil),
	"tcga-gdc":               NewRequest("tcga-gdc", "/projects", map[string]string{"size": "1"}, nil),
	"cbioportal":             NewRequest("cbioportal", "/studies/brca_tcga", nil, nil),
	"hca":                    NewRequest("hca", "/index/projects", map[string]string{"size": "1"}, nil),
	"rummageo":               NewRequest("rummageo", "", nil, map[string]any{"terms": []string{"neuron"}, "first": 1, "offset": 0}),
	"uniprot":                NewRequest("uniprot", "/uniprotkb/P04637.json", nil, nil),
	"ncbi-protein":           NewRequest("ncbi-protein", "/esearch.fcgi", map[string]string{"db": "protein", "term": "insulin", "retmax": "1", "retmode": "json"}, nil),
	"pdb":                    NewRequest("pdb", "/rest/v1/core/entry/4HHB", nil, nil),
	"alphafold":              NewRequest("alphafold", "/api/prediction/P00533", nil, nil),
	"emdb":                   NewRequest("emdb", "/entry/EMD-1234", nil, nil),
	"interpro":               NewRequest("interpro", "/entry/interpro/IPR000504", nil, nil),
	"string":                 NewRequest("string", "/json/get_string_ids", map[string]string{"identifiers": "TP53", "species": "9606"}, nil),
	"human-protein-atlas":    NewRequest("human-protein-atlas", "/ENSG00000141510.json", nil, nil),
	"pride":                  NewRequest("pride", "/projects", map[string]string{"pageSize": "1"}, nil),
	"reactome":               NewRequest("reactome", "/ContentService/data/database/version", nil, nil),
	"quickgo":                NewRequest("quickgo", "/ontology/go/terms/GO:0003723", nil, nil),
	"gene-ontology":          NewRequest("gene-ontology", "/ontology/term/GO:0008150", nil, nil),
	"pubchem":                NewRequest("pubchem", "/compound/name/aspirin/property/MolecularFormula/JSON", nil, nil),
	"chembl":                 NewRequest("chembl", "/molecule/CHEMBL25.json", nil, nil),
	"chebi":                  NewRequest("chebi", "/es_search/", map[string]string{"term": "glucose"}, nil),
	"bindingdb":              NewRequest("bindingdb", "/rest/getLigandsByUniprots", map[string]string{"uniprot": "P00533", "cutoff": "100", "response": "application/json"}, nil),
	"zinc":                   NewRequest("zinc", "/substances/ZINC000000000053.json", nil, nil),
	"clinpgx":                NewRequest("clinpgx", "/data/gene", map[string]string{"symbol": "CYP2D6"}, nil),
	"opentargets":            NewRequest("opentargets", "", nil, map[string]any{"ensemblId": "ENSG00000157764"}),
	"metabolomics-workbench": NewRequest("metabolomics-workbench", "/study/study_id/ST000001/summary", nil, nil),
	"hpo":                    NewRequest("hpo", "/hp/terms/HP:0001250", nil, nil),
	"monarch":                NewRequest("monarch", "/entity/MONDO:0007947", nil, nil),
	"mousemine":              NewRequest("mousemine", "/search", map[string]string{"q": "Brca1", "format": "json"}, nil),
	"cod":                    NewRequest("cod", "/result", map[string]string{"text": "quartz", "format": "json"}, nil),
	"nist":                   NewRequest("nist", "/cuu/Constants/Table/allascii.txt", nil, nil),
	"sdss":                   NewRequest("sdss", "/dr18/SkyServerWS/SearchTools/SqlSearch", map[string]string{"cmd": "SELECT TOP 1 objid FROM PhotoObj", "format": "json"}, nil),
	"simbad":                 NewRequest("simbad", "/sim-tap/sync", map[string]string{"request": "doQuery", "lang": "adql", "format": "json", "query": "SELECT TOP 1 main_id FROM basic"}, nil),
	"nasa-exoplanet-archive": NewRequest("nasa-exoplanet-archive", "/TAP/sync", map[string]string{"query": "SELECT TOP 1 pl_name FROM ps", "format": "json"}, nil),
	"arxiv":                  NewRequest("arxiv", "/query", map[string]string{"search_query": "all:transformer", "max_results": "1"}, nil),
	"biorxiv":                NewRequest("biorxiv", "/details/biorxiv/2024-01-01/2024-01-02/0", nil, nil),
	"medrxiv":                NewRequest("medrxiv", "/details/medrxiv/2024-01-01/2024-01-02/0", nil, nil),
	"biostudies":             NewRequest("biostudies", "/search", map[string]string{"query": "crispr", "pageSize": "1"}, nil),
	"crossref":               NewRequest("crossref", "/works/10.1038/nature12373", nil, nil),
	"doaj":                   NewRequest("doaj", "/search/articles/bibjson.title:CRISPR", map[string]string{"pageSize": "1"}, nil),
	"figshare":               NewRequest("figshare", "/articles", map[string]string{"page_size": "1"}, nil),
	"opencitations":          NewRequest("opencitations", "/index/v2/citation-count/doi:10.1038/nature12373", nil, nil),
	"pmc":                    NewRequest("pmc", "/efetch.fcgi", map[string]string{"db": "pmc", "id": "7029759", "retmode": "xml"}, nil),
	"pubmed":                 NewRequest("pubmed", "/esearch.fcgi", map[string]string{"db": "pubmed", "term": "CRISPR", "retmax": "1", "retmode": "json"}, nil),
	"pubtator":               NewRequest("pubtator", "/search/", map[string]string{"text": "BRCA1"}, nil),
	"ror":                    NewRequest("ror", "/organizations", map[string]string{"query": "oxford"}, nil),
	"semantic-scholar":       NewRequest("semantic-scholar", "/graph/v1/paper/DOI:10.1038/nature12373", map[string]string{"fields": "title"}, nil),
	"unpaywall":              NewRequest("unpaywall", "/10.1038/nature12373", nil, nil),
	"zenodo":                 NewRequest("zenodo", "/records", map[string]string{"q": "crispr", "size": "1"}, nil),
}

// TestLiveEveryOpenConnectorAnswers sends each canned request to the real API.
// Run: CONTACT_EMAIL=you@lab.org go test -tags live -run Live -v ./internal/connectors/
func TestLiveEveryOpenConnectorAnswers(t *testing.T) {
	c := &Client{HTTP: Guarded(), Contact: os.Getenv("CONTACT_EMAIL")}
	ws := uuid.New()
	for _, e := range Catalogue {
		if e.Tier == Restricted || (e.Tier == Key && !e.Keyless) {
			continue
		}
		t.Run(e.Name, func(t *testing.T) {
			r, ok := canned[e.Name]
			if !ok {
				t.Fatalf("%s has no canned request", e.Name)
			}
			res, err := c.Do(context.Background(), ws, e, r, "")
			if err != nil || res.Status != 200 || len(res.Body) == 0 {
				t.Fatalf("%s: %v status %d, %d bytes: %.300s", e.Name, err, res.Status, len(res.Body), res.Body)
			}
			// A GraphQL answer can be 200 and still carry only errors.
			if e.Body != "" && strings.Contains(string(res.Body), `"errors"`) {
				t.Fatalf("%s: GraphQL errors: %.300s", e.Name, res.Body)
			}
			t.Logf("%s: %d bytes, %s: %.120q", e.Name, len(res.Body), res.ContentType, res.Body)
		})
	}
}
