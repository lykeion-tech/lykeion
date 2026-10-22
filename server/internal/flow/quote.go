package flow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"golang.org/x/text/unicode/norm"

	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

// Call is a recorded database call as quote matching needs it: its whole
// answer, read from the blob when the record's body kept only a cut of it.
// Unreadable is set when that blob is gone, and Answer then holds only the
// cut the body kept.
type Call struct {
	Answer     string
	Unreadable bool
}

// QuoteMatch is one quote found in a record. Call is the cited call it was
// found in; it is empty when the quote matched none. Unchecked names a cited
// call whose full answer could not be read, for a quote that matched none.
type QuoteMatch struct {
	Record, Quote, Call string
	Matched             bool
	Unchecked           string
}

// normalize puts text in Unicode NFC and every run of whitespace to one
// space. Case, punctuation and wording are left as they are.
func normalize(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(s)), " ")
}

// LoadCalls reads the whole answer of each call record in recs, from the blob
// when the body was cut; a blob that is gone marks its call unreadable. It
// does blob I/O, so the caller runs it outside a transaction.
func LoadCalls(ctx context.Context, blobs storage.Storage, recs []db.Record) (map[string]Call, error) {
	calls := map[string]Call{}
	for _, rec := range recs {
		if rec.Kind != "call" {
			continue
		}
		var body struct {
			Answer string `json:"answer"`
		}
		if json.Unmarshal(rec.Body, &body) != nil {
			continue
		}
		if rec.BlobKey != "" {
			rc, err := blobs.Get(ctx, rec.BlobKey)
			if errors.Is(err, storage.ErrNotFound) {
				calls[rec.Key] = Call{Answer: body.Answer, Unreadable: true}
				continue
			}
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			body.Answer = string(b)
		}
		calls[rec.Key] = Call{Answer: body.Answer}
	}
	return calls, nil
}

// MatchQuotes checks each quote in recs against the calls its record cites.
// A quote is a string under a `quote` field, or in a `quotes` list, at any
// depth of a record's body, so a claim table's rows are covered. A quote is
// checked against the calls named by the `cites` of the nearest enclosing
// object that has one, and so a row can cite for itself.
func MatchQuotes(recs []db.Record, calls map[string]Call) []QuoteMatch {
	var out []QuoteMatch
	// Each call's answer, up to an attached file's whole text, is normalized
	// once however many quotes cite it.
	normalized := map[string]string{}
	answer := func(k string, c Call) string {
		n, ok := normalized[k]
		if !ok {
			n = normalize(c.Answer)
			normalized[k] = n
		}
		return n
	}
	for _, rec := range recs {
		var body any
		if json.Unmarshal(rec.Body, &body) != nil {
			continue
		}
		walkQuotes(body, nil, func(q string, cites []string) {
			m := QuoteMatch{Record: rec.Key, Quote: q}
			if nq := normalize(q); nq != "" {
				for _, k := range cites {
					c, ok := calls[k]
					if ok && strings.Contains(answer(k, c), nq) {
						m.Matched, m.Call, m.Unchecked = true, k, ""
						break
					}
					if ok && c.Unreadable && m.Unchecked == "" {
						m.Unchecked = k
					}
				}
			}
			out = append(out, m)
		})
	}
	return out
}

func walkQuotes(v any, cites []string, found func(quote string, cites []string)) {
	switch v := v.(type) {
	case []any:
		for _, e := range v {
			walkQuotes(e, cites, found)
		}
	case map[string]any:
		if list, ok := v["cites"].([]any); ok {
			cites = nil
			for _, c := range list {
				if s, ok := c.(string); ok {
					if k, ok := strings.CutPrefix(s, "call:"); ok {
						cites = append(cites, k)
					}
				}
			}
		}
		for name, e := range v {
			switch {
			case name == "quote":
				if s, ok := e.(string); ok {
					found(s, cites)
					continue
				}
			case name == "quotes":
				if list, ok := e.([]any); ok {
					for _, q := range list {
						if s, ok := q.(string); ok {
							found(s, cites)
						}
					}
					continue
				}
			}
			walkQuotes(e, cites, found)
		}
	}
}
