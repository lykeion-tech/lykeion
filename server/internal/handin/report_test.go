package handin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func reportJSON(t *testing.T, v map[string]any) []byte {
	t.Helper()
	base := map[string]any{"status": "done", "reason": nil, "concepts": []any{}, "facts": []any{}, "links": []any{}}
	for k, x := range v {
		base[k] = x
	}
	b, _ := json.Marshal(base)
	return b
}

func hasProblem(probs []string, sub string) bool {
	for _, p := range probs {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

func fact(rel string) map[string]any {
	return map[string]any{"subject": "a", "relation": rel, "object": "b", "quote": "q", "source_node": "question"}
}

func TestReportKindFor(t *testing.T) {
	if k, ok := KindFor("report", "agent", "Validation"); !ok || k != Report {
		t.Fatalf("got %v %v", k, ok)
	}
}

func TestReportSchemaAcceptsValid(t *testing.T) {
	h, probs := Parse(Report, reportJSON(t, map[string]any{"facts": []any{fact("increases")}}))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	_ = h
	r, _ := ParseReport(reportJSON(t, map[string]any{"facts": []any{fact("increases")}}))
	if len(r.Facts) != 1 {
		t.Fatal(r)
	}
}

func TestReportSchemaRejectsUnknownRelation(t *testing.T) {
	_, probs := Parse(Report, reportJSON(t, map[string]any{"facts": []any{fact("causes")}}))
	if !hasProblem(probs, "relation") {
		t.Fatal(probs)
	}
	if !strings.Contains(Schema(Report), `"is_a"`) {
		t.Fatal("schema lacks the relations")
	}
}

func TestReportSchemaRejectsTooManyFacts(t *testing.T) {
	var facts []any
	for i := 0; i < 81; i++ {
		facts = append(facts, fact("is_a"))
	}
	_, probs := Parse(Report, reportJSON(t, map[string]any{"facts": facts}))
	if !hasProblem(probs, "at most 80") {
		t.Fatal(probs)
	}
}

func TestReportSchemaRejectsExtraFieldAndLongQuote(t *testing.T) {
	_, probs := Parse(Report, reportJSON(t, map[string]any{"extra": 1}))
	if !hasProblem(probs, "extra") {
		t.Fatal(probs)
	}
	f := fact("is_a")
	f["quote"] = strings.Repeat("x", 601)
	_, probs = Parse(Report, reportJSON(t, map[string]any{"facts": []any{f}}))
	if !hasProblem(probs, fmt.Sprint(MaxQuoteBytes)) {
		t.Fatal(probs)
	}
}

func TestReportCouldNotDoNeedsReason(t *testing.T) {
	_, probs := Parse(Report, reportJSON(t, map[string]any{"status": "could_not_do"}))
	if !hasProblem(probs, "reason") {
		t.Fatal(probs)
	}
	_, probs = Parse(Report, reportJSON(t, map[string]any{"status": "could_not_do", "reason": "no"}))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
}

func TestReportConceptKeyIsCapped(t *testing.T) {
	concept := func(key string) map[string]any {
		return map[string]any{"concepts": []any{map[string]any{"key": key, "label": "L", "type": "gene"}}}
	}
	if _, probs := Parse(Report, reportJSON(t, concept(strings.Repeat("k", MaxConceptKeyBytes)))); len(probs) != 0 {
		t.Fatal(probs)
	}
	_, probs := Parse(Report, reportJSON(t, concept(strings.Repeat("k", MaxConceptKeyBytes+1))))
	if !hasProblem(probs, "key must be at most 80 bytes") {
		t.Fatal(probs)
	}
}
