package flow

import (
	"encoding/json"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

func quoteRec(key string, body map[string]any) db.Record {
	b, _ := json.Marshal(body)
	return db.Record{Key: key, Kind: "claim", Body: b}
}

func TestQuoteMatchIsWordForWordUpToWhitespace(t *testing.T) {
	calls := map[string]Call{
		"c1": {Answer: "The median survival was 14.2 months\nin the treated group, and 9 months in controls."},
		"c2": {Answer: "Something else entirely."},
	}
	for _, tc := range []struct {
		name, quote, cite string
		want              bool
	}{
		{"line break against a space", "median survival was 14.2 months in the treated group", "c1", true},
		{"double spaces", "14.2  months   in the  treated group", "c1", true},
		{"changed digit", "median survival was 14.3 months", "c1", false},
		{"inserted ellipsis", "median survival was … months in the treated group", "c1", false},
		{"different case", "Median Survival was 14.2 months", "c1", false},
		{"paraphrase", "survival was about fourteen months", "c1", false},
		{"another call's answer", "median survival was 14.2 months", "c2", false},
		{"empty quote", "   ", "c1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := quoteRec("k1", map[string]any{"cites": []string{"call:" + tc.cite}, "quote": tc.quote})
			got := MatchQuotes([]db.Record{rec}, calls)
			if len(got) != 1 || got[0].Matched != tc.want || got[0].Record != "k1" || got[0].Quote != tc.quote {
				t.Fatalf("got %+v, want matched=%v", got, tc.want)
			}
			if tc.want && got[0].Call != tc.cite {
				t.Fatalf("matched call %q", got[0].Call)
			}
		})
	}
}

func TestQuotesInNestedRowsUseTheirOwnCites(t *testing.T) {
	calls := map[string]Call{"c1": {Answer: "alpha beta"}, "c2": {Answer: "gamma delta"}}
	rec := quoteRec("k", map[string]any{"cites": []string{"call:c1"}, "rows": []any{
		map[string]any{"quote": "alpha  beta"},
		map[string]any{"quotes": []string{"gamma delta"}, "cites": []string{"call:c2"}},
		map[string]any{"quote": "gamma delta"},
	}})
	got := MatchQuotes([]db.Record{rec}, calls)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	// the first row inherits the record's cite; the second cites c2 itself;
	// the third inherits c1, which does not hold its quote
	for i, want := range []QuoteMatch{{"k", "alpha  beta", "c1", true, ""}, {"k", "gamma delta", "c2", true, ""}, {"k", "gamma delta", "", false, ""}} {
		found := false
		for _, m := range got {
			found = found || m == want
		}
		if !found {
			t.Fatalf("row %d: no %+v in %+v", i, want, got)
		}
	}
}
