package handin

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

func rec(col, kind, key string, body map[string]any) db.Record {
	b, _ := json.Marshal(body)
	return db.Record{BoardColumn: col, Kind: kind, Key: key, Body: b}
}

func TestStandingHypothesesKeepTheAcceptedTypedDeclaration(t *testing.T) {
	original := rec(board.Hypotheses, review.KindHypothesis, "h1", map[string]any{
		"text": "Accuracy exceeds the target", "measure": review.Measure{ID: "accuracy", Name: "Accuracy", Unit: "fraction"},
		"comparison": map[string]any{"threshold": 0.5},
	})
	original.Role, original.Round = "leader", 1
	replacement := rec(board.Hypotheses, review.KindHypothesis, "h2", map[string]any{
		"text": "Accuracy  exceeds the target", "comparison": map[string]any{"threshold": 0.4},
	})
	replacement.Role, replacement.Round = "leader", 2
	newTyped := rec(board.Hypotheses, review.KindHypothesis, "h3", map[string]any{
		"text": "Recall exceeds the target", "comparison": map[string]any{"threshold": 0.5},
	})
	newTyped.Role, newTyped.Round = "leader", 2
	legacy := rec(board.Hypotheses, review.KindHypothesis, "h4", map[string]any{"text": "Accuracy increases"})
	legacy.Role, legacy.Round = "leader", 2
	result := rec(board.Hypotheses, "review", "review", map[string]any{
		"items": []review.Item{{Kind: review.KindHypothesis, Key: original.Key, Status: review.Pass, Carried: true}},
	})
	result.Role, result.Round = "server", 2
	outcome := rec(board.Hypotheses, "outcome", "outcome", map[string]any{"verdict": review.DecidePass})
	outcome.Role, outcome.Round = "server", 2
	study := []db.Record{original, replacement, newTyped, legacy, result, outcome}
	var keys []string
	for _, r := range NewEvidence(study).Standing() {
		keys = append(keys, r.Key)
	}
	if !slices.Equal(keys, []string{original.Key, legacy.Key}) {
		t.Fatalf("standing hypotheses = %v, want the accepted typed and legacy records", keys)
	}
}

func TestEvidenceMatchesSpellings(t *testing.T) {
	gone := time.Now()
	old := rec(board.Grounding, "call", "c9", map[string]any{"answer": "doi:10.9999/old"})
	old.SupersededAt = &gone
	e := NewEvidence([]db.Record{
		rec(board.Grounding, "search", "", map[string]any{"results": []string{"doi:10.1000/abc", "arxiv:2101.00001"}}),
		rec(board.Grounding, "call", "c1", map[string]any{"answer": `{"doi":"10.2000/xyz"} see (https://doi.org/10.3000/q-1).`}),
		rec(board.Grounding, "call", "c2", map[string]any{"server": records.AttachmentServer, "answer": "ref 10.4000/att;"}),
		rec(board.Grounding, "open_dataset", "GSE1", map[string]any{"accession": "GSE1"}),
		old,
	})
	for _, id := range []string{"https://doi.org/10.1000/ABC", "10.1000/abc", "DOI:10.1000/abc"} {
		if len(e.Sources(id)) == 0 {
			t.Errorf("%s not found", id)
		}
	}
	for _, id := range []string{"doi:10.1000/abd", "doi:10.1000/ab", "doi:10.2000/xy", "doi:10.9999/old"} {
		if s := e.Sources(id); s != nil {
			t.Errorf("%s found in %v", id, s)
		}
	}
	if s := e.Sources("doi:10.48550/arXiv.2101.00001"); len(s) != 1 {
		t.Errorf("arxiv doi form: %v", s)
	}
	if s := e.Sources("doi:10.2000/xyz"); len(s) != 1 || s[0] != "call:c1" {
		t.Errorf("call answer: %v", s)
	}
	if s := e.Sources("10.3000/q-1"); len(s) != 1 || s[0] != "call:c1" {
		t.Errorf("url in parens: %v", s)
	}
	if s := e.Sources("10.4000/att"); len(s) != 1 || s[0] != "call:c2" {
		t.Errorf("attachment: %v", s)
	}
	if len(e.Sources("gse1")) == 0 {
		t.Error("accession not found")
	}
}

func TestEvidenceIgnoresAgentWrittenText(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Grounding, "paper", "r1", map[string]any{"id": "doi:10.1000/p", "text": "see doi:10.5000/own"}),
	})
	if s := e.Sources("doi:10.5000/own"); s != nil {
		t.Errorf("agent text counted: %v", s)
	}
	if s := e.Sources("doi:10.1000/p"); s != nil {
		t.Errorf("paper id counted as source: %v", s)
	}
	if k, ok := e.Grounding("https://doi.org/10.1000/P"); !ok || k != "r1" {
		t.Errorf("grounding: %q %v", k, ok)
	}
}

func TestEvidenceRealSpellings(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Grounding, "search", "", map[string]any{"results": []string{"doi:10.1/abc", "arxiv:2101.00001"}}),
		rec(board.Grounding, "call", "c1", map[string]any{"answer": "is it 10.2/def? yes! also 10.3/ghi."}),
	})
	for _, id := range []string{"https://arxiv.org/abs/2101.00001", "https://arxiv.org/pdf/2101.00001v2.pdf", "arXiv:2101.00001v2",
		"https://doi.org/10.1/abc?x=1", "https://doi.org/10.1/ABC#frag", "10.2/def", "10.3/ghi"} {
		if len(e.Sources(id)) == 0 {
			t.Errorf("%s not found", id)
		}
	}
	if e.Sources("arxiv:2101.0000") != nil {
		t.Error("arxiv prefix matched")
	}
}

func TestEvidenceCountsEarlierColumn(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Grounding, "call", "c1", map[string]any{"answer": "doi:10.7/early"}),
		rec(board.Hypotheses, "paper", "r2", map[string]any{"id": "doi:10.7/other"}),
	})
	if s := e.Sources("doi:10.7/early"); len(s) != 1 || s[0] != "call:c1" {
		t.Errorf("earlier column: %v", s)
	}
	if _, ok := e.Grounding("doi:10.7/other"); ok {
		t.Error("a Hypotheses paper is not Grounding")
	}
}

// A sources answer names a paper's PubMed id in its pmid field; a hand-in
// names it pmid:N. Neither a shorter number nor a bare number in an answer
// counts.
func TestEvidenceFindsPubMedIdsInCallAnswers(t *testing.T) {
	e := NewEvidence([]db.Record{
		rec(board.Grounding, "call", "c1", map[string]any{"answer": `{"results":[{"id":"europepmc:MED:35872898","pmid":"35872898","title":"x"}]}`}),
		rec(board.Grounding, "call", "c2", map[string]any{"answer": `cohort of 4242 mice`}),
	})
	for _, id := range []string{"pmid:35872898", "PMID:35872898"} {
		if s := e.Sources(id); len(s) != 1 || s[0] != "call:c1" {
			t.Errorf("%s: %v", id, s)
		}
	}
	for _, id := range []string{"pmid:3587289", "pmid:4242"} {
		if s := e.Sources(id); s != nil {
			t.Errorf("%s found in %v", id, s)
		}
	}
}
