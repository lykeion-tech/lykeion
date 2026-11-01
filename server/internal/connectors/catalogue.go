// Package connectors is the catalogue of data sources an agent may fetch
// through the sources server, and the client that fetches from them. The
// server, never the agent, decides the host of every request.
package connectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	db "lykeion/server/pkg/db/generated"
)

const (
	Open       = "open"       // enabled unless the workspace disables it
	Key        = "key"        // enabled once the workspace saves a key, or keyless when Keyless
	Restricted = "restricted" // enabled only after the owner confirms the source's licence
)

const (
	Genomics   = "Genomics and variants"
	Proteins   = "Proteins and structures"
	Chemistry  = "Chemistry and drugs"
	Phenotype  = "Phenotype and disease"
	Materials  = "Materials and physics"
	Astronomy  = "Astronomy"
	Literature = "Literature"
)

var Domains = []string{Genomics, Proteins, Chemistry, Phenotype, Materials, Astronomy, Literature}

// Entry is one connector. Every request goes to Base plus the agent's path;
// redirects may only reach Hosts. Body is the fixed JSON of a POST: its
// string values of the form "$name" are the only slots the agent fills.
type Entry struct {
	Name, Domain, Purpose, Tier string
	Base                        string
	Hosts                       []string
	Methods                     []string
	Body                        string
	KeyHeader, KeyQuery         string // where the server puts the workspace's key
	Keyless                     bool   // a key connector that also works without one
	Contact                     string // query parameter carrying the server's contact email
	ContactRequired             bool
	Rate                        float64 // requests per second, per workspace
	Terms                       string
}

// Variables are the names of Body's slots.
func (e Entry) Variables() []string {
	var v any
	json.Unmarshal([]byte(e.Body), &v)
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			if name, ok := strings.CutPrefix(t, "$"); ok && name != "" {
				out = append(out, name)
			}
		case map[string]any:
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	slices.Sort(out)
	return slices.Compact(out)
}

func get(name, domain, tier, base string, rate float64, terms, purpose string) Entry {
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	return Entry{Name: name, Domain: domain, Tier: tier, Base: base, Hosts: []string{u.Hostname()}, Methods: []string{"GET"},
		Rate: rate, Terms: terms, Purpose: purpose}
}

func (e Entry) key(header, query string) Entry { e.KeyHeader, e.KeyQuery = header, query; return e }
func (e Entry) keyless() Entry                 { e.Keyless = true; return e }
func (e Entry) post(body string) Entry {
	e.Methods, e.Body = append(slices.Clone(e.Methods), "POST"), body
	return e
}
func (e Entry) postOnly(body string) Entry { e.Methods, e.Body = []string{"POST"}, body; return e }
func (e Entry) also(hosts ...string) Entry {
	e.Hosts = append(slices.Clone(e.Hosts), hosts...)
	return e
}
func (e Entry) contact(param string, required bool) Entry {
	e.Contact, e.ContactRequired = param, required
	return e
}

const (
	eutils   = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"
	ncbiTerm = "https://www.ncbi.nlm.nih.gov/home/about/policies/"
	ebiTerm  = "https://www.ebi.ac.uk/about/terms-of-use/"
)

// Catalogue is every connector. Tiers, rates and terms links are provisional
// until the live check re-reads each source's current terms; a rate of 1 is
// the conservative choice where the source documents none.
var Catalogue = []Entry{
	get("ensembl", Genomics, Open, "https://rest.ensembl.org", 15, "https://www.ensembl.org/info/about/legal/", "Genes, transcripts, variants and their consequences across vertebrate genomes."),
	get("ucsc-genome", Genomics, Open, "https://api.genome.ucsc.edu", 1, "https://genome.ucsc.edu/conditions.html", "Genome sequence and annotation tracks by assembly and region."),
	get("ncbi-gene", Genomics, Key, eutils, 3, ncbiTerm, "Gene records and summaries through E-utilities (db=gene).").key("", "api_key").keyless(),
	get("ncbi-taxonomy", Genomics, Key, eutils, 3, ncbiTerm, "Organism names, lineages and taxonomy ids (db=taxonomy).").key("", "api_key").keyless(),
	get("ena", Genomics, Open, "https://www.ebi.ac.uk/ena", 1, ebiTerm, "European Nucleotide Archive studies, samples, runs and their files."),
	get("sra", Genomics, Key, eutils, 3, ncbiTerm, "Sequencing run metadata: experiments, samples and platforms (db=sra).").key("", "api_key").keyless(),
	get("geo", Genomics, Key, eutils, 3, ncbiTerm, "Gene Expression Omnibus series and datasets (db=gds).").key("", "api_key").keyless(),
	get("encode", Genomics, Open, "https://www.encodeproject.org", 1, "https://www.encodeproject.org/help/citing-encode/", "ENCODE experiments and files: ChIP-seq, ATAC-seq, RNA-seq and more."),
	get("gtex", Genomics, Open, "https://gtexportal.org/api/v2", 1, "https://gtexportal.org/home/license", "Tissue expression and eQTLs from GTEx."),
	get("gnomad", Genomics, Open, "https://gnomad.broadinstitute.org/api", 1, "https://gnomad.broadinstitute.org/terms", "Population allele frequencies of one variant (GraphQL).").
		postOnly(`{"query":"query ($variantId: String!, $dataset: DatasetId!) { variant(variantId: $variantId, dataset: $dataset) { variant_id rsids chrom pos ref alt exome { ac an af } genome { ac an af } } }","variables":{"variantId":"$variantId","dataset":"$dataset"}}`),
	get("dbsnp", Genomics, Key, eutils, 3, ncbiTerm, "Reference SNP records (db=snp).").key("", "api_key").keyless(),
	get("clinvar", Genomics, Key, eutils, 3, ncbiTerm, "Clinical significance of variants (db=clinvar).").key("", "api_key").keyless(),
	get("myvariant", Genomics, Open, "https://myvariant.info/v1", 1, "https://myvariant.info", "Aggregated variant annotations by HGVS id."),
	get("gwas-catalog", Genomics, Open, "https://www.ebi.ac.uk/gwas/rest/api/v2", 1, ebiTerm, "GWAS Catalog studies, associations and SNPs."),
	get("regulomedb", Genomics, Open, "https://api.regulomedb.org", 1, "https://regulomedb.org/regulome-help/", "Regulatory annotation scores for variants."),
	get("jaspar", Genomics, Open, "https://jaspar.elixir.no/api/v1", 1, "https://jaspar.elixir.no/about/", "Transcription factor binding profiles."),
	get("tcga-gdc", Genomics, Open, "https://api.gdc.cancer.gov", 1, "https://gdc.cancer.gov/access-data/data-access-policies", "NCI Genomic Data Commons projects, cases, files and mutations (open data)."),
	get("cbioportal", Genomics, Open, "https://www.cbioportal.org/api", 1, "https://www.cbioportal.org/faq", "Cancer genomics studies, mutations and clinical data."),
	get("hca", Genomics, Open, "https://service.azul.data.humancellatlas.org", 1, "https://data.humancellatlas.org/about/data-use-agreement", "Human Cell Atlas projects, samples and files."),
	get("rummageo", Genomics, Open, "https://rummageo.com/graphql", 1, "https://rummageo.com", "GEO-derived gene-set signatures found by term (GraphQL).").
		postOnly(`{"query":"query ($terms: [String]!, $first: Int!, $offset: Int!) { geneSetTermSearch(terms: $terms, first: $first, offset: $offset) { totalCount nodes { term } } }","variables":{"terms":"$terms","first":"$first","offset":"$offset"}}`),

	get("uniprot", Proteins, Open, "https://rest.uniprot.org", 1, "https://www.uniprot.org/help/license", "Protein sequences, functions and identifier mapping."),
	get("ncbi-protein", Proteins, Key, eutils, 3, ncbiTerm, "Protein sequence records (db=protein).").key("", "api_key").keyless(),
	get("pdb", Proteins, Open, "https://data.rcsb.org", 1, "https://www.rcsb.org/pages/usage-policy", "Structure entries, entities and assemblies from RCSB PDB."),
	get("alphafold", Proteins, Open, "https://alphafold.ebi.ac.uk", 1, "https://alphafold.ebi.ac.uk/faq", "Predicted structures and their confidence by UniProt accession."),
	get("emdb", Proteins, Open, "https://www.ebi.ac.uk/emdb/api", 1, ebiTerm, "Electron microscopy maps and their metadata."),
	get("interpro", Proteins, Open, "https://www.ebi.ac.uk/interpro/api", 1, ebiTerm, "Protein families, domains and sites."),
	get("string", Proteins, Open, "https://string-db.org/api", 1, "https://string-db.org/cgi/access", "Protein-protein interaction networks and their scores."),
	get("biogrid", Proteins, Key, "https://webservice.thebiogrid.org", 1, "https://thebiogrid.org", "Curated genetic and protein interactions.").key("", "accesskey"),
	get("human-protein-atlas", Proteins, Open, "https://www.proteinatlas.org", 1, "https://www.proteinatlas.org/about/licence", "Tissue and cell expression of human proteins."),
	get("pride", Proteins, Open, "https://www.ebi.ac.uk/pride/ws/archive/v3", 1, ebiTerm, "Proteomics datasets and their files."),
	get("reactome", Proteins, Open, "https://reactome.org", 1, "https://reactome.org/license", "Pathways, reactions and their participants (ContentService)."),
	get("quickgo", Proteins, Open, "https://www.ebi.ac.uk/QuickGO/services", 1, ebiTerm, "Gene Ontology terms and annotations."),
	get("gene-ontology", Proteins, Open, "https://api.geneontology.org/api", 1, "https://geneontology.org/docs/go-citation-policy/", "GO terms and gene function annotations from the GO API."),

	get("pubchem", Chemistry, Open, "https://pubchem.ncbi.nlm.nih.gov/rest/pug", 5, ncbiTerm, "Compounds, properties and bioassays."),
	get("chembl", Chemistry, Open, "https://www.ebi.ac.uk/chembl/api/data", 5, ebiTerm, "Bioactive molecules, targets and assay results."),
	get("chebi", Chemistry, Open, "https://www.ebi.ac.uk/chebi/backend/api/public", 1, ebiTerm, "Chemical entities of biological interest."),
	get("bindingdb", Chemistry, Open, "https://bindingdb.org", 1, "https://www.bindingdb.org", "Measured binding affinities of ligands and targets.").also("www.bindingdb.org"),
	get("zinc", Chemistry, Open, "https://zinc.docking.org", 1, "https://zinc.docking.org", "Purchasable compounds for virtual screening."),
	get("clinpgx", Chemistry, Open, "https://api.clinpgx.org/v1", 2, "https://www.clinpgx.org", "Pharmacogenomic annotations of genes, variants and drugs."),
	get("opentargets", Chemistry, Open, "https://api.platform.opentargets.org/api/v4/graphql", 1, "https://platform-docs.opentargets.org/licence", "A target by Ensembl id with its associated diseases (GraphQL).").
		postOnly(`{"query":"query ($ensemblId: String!) { target(ensemblId: $ensemblId) { id approvedSymbol approvedName biotype associatedDiseases(page: {index: 0, size: 25}) { count rows { score disease { id name } } } } }","variables":{"ensemblId":"$ensemblId"}}`),
	get("lincs-l1000", Chemistry, Key, "https://api.clue.io/api", 1, "https://clue.io/terms", "L1000 perturbation signatures and their metadata.").key("user_key", ""),
	get("metabolomics-workbench", Chemistry, Open, "https://www.metabolomicsworkbench.org/rest", 1, "https://www.metabolomicsworkbench.org", "Metabolomics studies, compounds and measurements."),
	get("drugbank", Chemistry, Restricted, "https://api.drugbank.com/discovery/v1", 1, "https://go.drugbank.com/terms", "Drugs, targets and interactions (licensed).").key("Authorization", ""),
	get("kegg", Chemistry, Restricted, "https://rest.kegg.jp", 3, "https://www.kegg.jp/kegg/legal.html", "Pathways, genes and compounds (academic use; commercial use needs a licence)."),

	get("hpo", Phenotype, Open, "https://ontology.jax.org/api", 1, "https://hpo.jax.org/app/license", "Human Phenotype Ontology terms and annotations."),
	get("monarch", Phenotype, Open, "https://api.monarchinitiative.org/v3/api", 1, "https://monarchinitiative.org", "Disease, phenotype and gene associations across species."),
	get("mousemine", Phenotype, Open, "https://www.mousemine.org/mousemine/service", 1, "https://www.informatics.jax.org/mgihome/other/copyright.shtml", "Mouse genes, alleles and phenotypes (MGI)."),
	get("omim", Phenotype, Restricted, "https://api.omim.org/api", 1, "https://omim.org/help/agreement", "Mendelian disorders and their genes (licensed).").key("", "apiKey"),
	get("cosmic", Phenotype, Restricted, "https://cancer.sanger.ac.uk/api", 1, "https://cancer.sanger.ac.uk/cosmic/license", "Somatic mutations in cancer (licensed).").key("Authorization", ""),
	get("disgenet", Phenotype, Restricted, "https://www.disgenet.com", 1, "https://www.disgenet.com/Plans", "Gene-disease and variant-disease associations (licensed).").key("Authorization", ""),

	get("materials-project", Materials, Key, "https://api.materialsproject.org", 25, "https://materialsproject.org", "Computed properties of inorganic materials.").key("X-API-KEY", ""),
	get("cod", Materials, Open, "https://www.crystallography.net/cod", 1, "https://www.crystallography.net/cod/", "Crystal structures of organic, inorganic and metal-organic compounds."),
	get("nist", Materials, Open, "https://physics.nist.gov", 1, "https://www.nist.gov/open/license", "Physical constants and atomic spectra (text pages)."),

	get("sdss", Astronomy, Open, "https://skyserver.sdss.org", 1, "https://www.sdss.org/collaboration/citing-sdss/", "Spectra and photometry through SkyServer SQL search."),
	get("simbad", Astronomy, Open, "https://simbad.cds.unistra.fr/simbad", 1, "https://simbad.cds.unistra.fr/simbad/", "Astronomical objects, identifiers and measurements (TAP)."),
	get("nasa-exoplanet-archive", Astronomy, Open, "https://exoplanetarchive.ipac.caltech.edu", 1, "https://exoplanetarchive.ipac.caltech.edu/docs/acknowledge.html", "Confirmed exoplanets and their parameters (TAP)."),

	get("arxiv", Literature, Open, "https://export.arxiv.org/api", 1.0/3, "https://info.arxiv.org/help/api/tou.html", "Preprints in physics, mathematics, computer science and more (Atom)."),
	get("biorxiv", Literature, Open, "https://api.biorxiv.org", 1, "https://www.biorxiv.org/about-biorxiv", "Biology preprints and their publication status."),
	get("medrxiv", Literature, Open, "https://api.biorxiv.org", 1, "https://www.medrxiv.org/content/about-medrxiv", "Health sciences preprints (paths name medrxiv)."),
	get("biostudies", Literature, Open, "https://www.ebi.ac.uk/biostudies/api/v1", 1, ebiTerm, "Study records and their supplementary files."),
	get("core", Literature, Key, "https://api.core.ac.uk/v3", 1, "https://core.ac.uk/terms", "Open access research outputs and their full texts.").key("", "api_key"),
	get("crossref", Literature, Open, "https://api.crossref.org", 5, "https://www.crossref.org/documentation/retrieve-metadata/rest-api/", "DOI metadata: works, funders and journals.").contact("mailto", false),
	get("doaj", Literature, Open, "https://doaj.org/api", 2, "https://doaj.org/terms/", "Open access journals and their articles."),
	get("figshare", Literature, Open, "https://api.figshare.com/v2", 1, "https://figshare.com/terms", "Research datasets, figures and their files.").
		post(`{"search_for":"$search_for","page":"$page","page_size":"$page_size"}`),
	get("opencitations", Literature, Open, "https://api.opencitations.net", 3, "https://opencitations.net/about", "Citation links and counts between works."),
	get("pmc", Literature, Open, eutils, 3, ncbiTerm, "PubMed Central full texts of open articles (db=pmc)."),
	get("pubmed", Literature, Open, eutils, 3, ncbiTerm, "PubMed citations and abstracts (db=pubmed)."),
	get("pubtator", Literature, Open, "https://www.ncbi.nlm.nih.gov/research/pubtator3-api", 3, ncbiTerm, "Genes, diseases and chemicals annotated in PubMed text."),
	get("ror", Literature, Open, "https://api.ror.org/v2", 6, "https://ror.org/about/faqs/", "Research organisations and their identifiers."),
	get("semantic-scholar", Literature, Key, "https://api.semanticscholar.org", 1, "https://www.semanticscholar.org/product/api/license", "Papers, authors and citations from the Semantic Scholar graph.").key("x-api-key", "").keyless(),
	get("unpaywall", Literature, Open, "https://api.unpaywall.org/v2", 1, "https://unpaywall.org/products/api", "Open access copies of a DOI.").contact("email", true),
	get("zenodo", Literature, Open, "https://zenodo.org/api", 0.5, "https://about.zenodo.org/terms/", "Records, datasets and software deposited in Zenodo."),
}

// OpenAlex is the sources server's own literature search, not a catalogue
// connector: a workspace may enable it and save a key for its daily budget.
var OpenAlex = Entry{Name: "openalex", Domain: Literature, Tier: Key, KeyQuery: "api_key", Keyless: true,
	Purpose: "Papers, preprints and abstracts across all fields, searched through the sources server."}

func Get(name string) (Entry, bool) {
	i := slices.IndexFunc(Catalogue, func(e Entry) bool { return e.Name == name })
	if i < 0 {
		return Entry{}, false
	}
	return Catalogue[i], true
}

// Ready says whether a workspace may fetch from e: row is its stored choice,
// nil when there is none, and contact the server's contact email.
func Ready(e Entry, row *db.WorkspaceConnector, contact string) error {
	if e.ContactRequired && contact == "" {
		return errors.New(e.Name + " needs the server's contact email: the server operator sets CONTACT_EMAIL")
	}
	hasKey := row != nil && len(row.KeyCiphertext) > 0
	needsKey := (e.KeyHeader != "" || e.KeyQuery != "") && !e.Keyless
	if e.Tier == Restricted && (row == nil || row.LicenceConfirmedAt == nil) {
		return fmt.Errorf("the workspace owner confirms the licence of `%s` in Connectors", e.Name)
	}
	if needsKey && !hasKey {
		return fmt.Errorf("the workspace owner saves a key for `%s` in Connectors", e.Name)
	}
	return nil
}
