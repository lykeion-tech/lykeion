package sources

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const GeoMetadataBase = "https://www.ncbi.nlm.nih.gov/geo/query/acc.cgi"
const geoMetadataLimit = 2 * 1024 * 1024

var geoAccession = regexp.MustCompile(`^(GSE|GSM)[0-9]+$`)

type MetadataRef struct {
	SourceID  string `json:"source_id"`
	Accession string `json:"accession"`
	Relation  string `json:"relation"`
}

type DatasetDocument struct {
	Structure       string
	MetadataSources []MetadataRef
}

// Metadata retrieves only the requested record's metadata, never linked files.
func (g GEO) Metadata(ctx context.Context, accession string) (Doc, error) {
	if !geoAccession.MatchString(accession) {
		return Doc{}, fmt.Errorf("geo: invalid metadata accession %q", accession)
	}
	base := g.MetadataBase
	if base == "" {
		base = GeoMetadataBase
	}
	u, err := url.Parse(base)
	if err != nil {
		return Doc{}, err
	}
	q := u.Query()
	q.Set("acc", accession)
	q.Set("targ", "self")
	q.Set("form", "text")
	q.Set("view", "full")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Doc{}, err
	}
	h := g.HTTP
	if h == nil {
		h = client
	}
	bounded := *h
	bounded.Timeout = CallTimeout
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return Doc{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Doc{}, fmt.Errorf("geo metadata: %s (Retry-After: %s)", resp.Status, resp.Header.Get("Retry-After"))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, geoMetadataLimit+1))
	if err != nil {
		return Doc{}, err
	}
	truncated := len(b) > geoMetadataLimit
	if truncated {
		b = b[:geoMetadataLimit]
	}
	// Only newline-terminated lines enter the projection.
	if end := strings.LastIndexByte(string(b), '\n'); end >= 0 {
		b = b[:end+1]
	} else {
		b = nil
	}
	text, err := projectGEOMetadata(string(b), accession)
	if err != nil {
		return Doc{}, err
	}
	return Doc{Hit: Hit{ID: "geo:" + accession}, Text: text, SourceKind: "dataset_metadata", SourceURL: u.String(), Truncated: truncated}, nil
}

func projectGEOMetadata(body, accession string) (string, error) {
	entity := "SAMPLE"
	prefix := "!Sample_"
	table := "!sample_table_"
	if strings.HasPrefix(accession, "GSE") {
		entity = "SERIES"
		prefix = "!Series_"
		table = "!series_table_"
	}
	lines := strings.SplitAfter(body, "\n")
	var out strings.Builder
	found, metadata, inTable, continuation := false, false, false, false
	for _, raw := range lines {
		if raw == "" {
			continue
		}
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if !found {
			if line == "" {
				continue
			}
			if line != "^"+entity+" = "+accession {
				return "", fmt.Errorf("geo metadata: missing or mismatched SOFT entity")
			}
			found = true
			out.WriteString(raw)
			continue
		}
		if strings.HasPrefix(line, "^") {
			break
		}
		if line == table+"begin" {
			inTable = true
			continuation = false
			continue
		}
		if line == table+"end" {
			inTable = false
			continue
		}
		if inTable {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			key, _, ok := strings.Cut(line, " = ")
			if !ok || len(key) == len(prefix) {
				return "", fmt.Errorf("geo metadata: malformed SOFT field")
			}
			out.WriteString(raw)
			metadata = true
			continuation = true
			continue
		}
		if continuation && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out.WriteString(raw)
			continue
		}
		if line == "" {
			continuation = false
			continue
		}
		return "", fmt.Errorf("geo metadata: malformed SOFT content")
	}
	if !found || !metadata {
		return "", fmt.Errorf("geo metadata: empty metadata projection")
	}
	return out.String(), nil
}
