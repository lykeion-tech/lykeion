package connectors

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

var slug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func TestEveryEntryIsComplete(t *testing.T) {
	seen := map[string]bool{}
	data, lit := 0, 0
	for _, e := range Catalogue {
		if seen[e.Name] || !slug.MatchString(e.Name) {
			t.Errorf("%q: duplicate or not a slug", e.Name)
		}
		seen[e.Name] = true
		u, err := url.Parse(e.Base)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			t.Errorf("%s: base %q is not a plain HTTPS URL", e.Name, e.Base)
		}
		if !slices.Contains(e.Hosts, u.Hostname()) {
			t.Errorf("%s: base host %s is not among its hosts %v", e.Name, u.Hostname(), e.Hosts)
		}
		for _, h := range e.Hosts {
			if strings.ContainsAny(h, ":/@") || h == "" {
				t.Errorf("%s: host %q", e.Name, h)
			}
		}
		if tu, err := url.Parse(e.Terms); err != nil || tu.Scheme != "https" {
			t.Errorf("%s: terms link %q", e.Name, e.Terms)
		}
		if !slices.Contains([]string{Open, Key, Restricted}, e.Tier) || !slices.Contains(Domains, e.Domain) || e.Purpose == "" || e.Rate <= 0 {
			t.Errorf("%s: tier %q, domain %q, purpose %q, rate %v", e.Name, e.Tier, e.Domain, e.Purpose, e.Rate)
		}
		if len(e.Methods) == 0 {
			t.Errorf("%s: no method", e.Name)
		}
		for _, m := range e.Methods {
			if m != "GET" && m != "POST" {
				t.Errorf("%s: method %s", e.Name, m)
			}
		}
		if slices.Contains(e.Methods, "POST") != (e.Body != "") {
			t.Errorf("%s: a POST entry needs a fixed body, and only a POST entry has one", e.Name)
		}
		if e.Body != "" {
			var v any
			if err := json.Unmarshal([]byte(e.Body), &v); err != nil || len(e.Variables()) == 0 {
				t.Errorf("%s: body %v, variables %v", e.Name, err, e.Variables())
			}
		}
		if e.Tier == Key && e.KeyHeader == "" && e.KeyQuery == "" {
			t.Errorf("%s: a key connector names where its key goes", e.Name)
		}
		if e.Keyless && e.Tier != Key {
			t.Errorf("%s: only a key connector can work keyless", e.Name)
		}
		if e.Domain == Literature {
			lit++
		} else {
			data++
		}
	}
	if data != 56 || lit != 16 {
		t.Fatalf("%d data connectors and %d literature connectors, want 56 and 16", data, lit)
	}
	for _, gone := range []string{"openalex", "europepmc", "fred", "clinicaltrials", "brenda", "noaa"} {
		if _, ok := Get(gone); ok {
			t.Errorf("%s is not a connector", gone)
		}
	}
}

func TestEachTierIsReadyOnlyWhenTheWorkspaceAllowsIt(t *testing.T) {
	now, who := time.Now(), uuid.New()
	sealed := []byte("0123456789012345678901234567890")
	row := func(enabled bool, key []byte, licensed bool) *db.WorkspaceConnector {
		r := &db.WorkspaceConnector{Enabled: enabled, KeyCiphertext: key}
		if licensed {
			r.LicenceConfirmedAt, r.LicenceConfirmedBy = &now, &who
		}
		return r
	}
	get := func(n string) Entry {
		e, ok := Get(n)
		if !ok {
			t.Fatalf("no %s", n)
		}
		return e
	}
	for _, c := range []struct {
		name, connector string
		row             *db.WorkspaceConnector
		contact         string
		want            string // "" for ready
	}{
		{"open with no row", "ensembl", nil, "", ""},
		{"open, disabled", "ensembl", row(false, nil, false), "", "the workspace owner enables `ensembl` in Connectors"},
		{"key, none saved", "materials-project", nil, "", "saves a key for `materials-project`"},
		{"key, saved", "materials-project", row(true, sealed, false), "", ""},
		{"key, works keyless", "ncbi-gene", nil, "", ""},
		{"restricted, not confirmed", "kegg", nil, "", "confirms the licence of `kegg`"},
		{"restricted, confirmed", "kegg", row(true, nil, true), "", ""},
		{"restricted, confirmed, no key", "omim", row(true, nil, true), "", "saves a key for `omim`"},
		{"restricted, confirmed, keyed", "omim", row(true, sealed, true), "", ""},
		{"restricted, confirmed, disabled", "omim", row(false, sealed, true), "", "enables `omim`"},
		{"contact missing", "unpaywall", nil, "", "CONTACT_EMAIL"},
		{"contact set", "unpaywall", nil, "lab@example.org", ""},
	} {
		err := Ready(get(c.connector), c.row, c.contact)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	for _, e := range Catalogue {
		if e.Tier == Restricted && Ready(e, row(true, sealed, false), "x@y.z") == nil {
			t.Errorf("%s is restricted but ready without a confirmed licence", e.Name)
		}
	}
}
