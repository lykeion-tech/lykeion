package handin

import "testing"

func TestManualDraftHandinPurposeIsolation(t *testing.T) {
	valid := []byte(`{"message":"Proposed note","draft":{"title":"Review calibration","context":"","deadline":null,"card_id":null},"statements":[]}`)
	if _, p := Parse(Kind("manual_decision_draft"), valid); len(p) > 0 {
		t.Fatalf("valid draft rejected: %v", p)
	}
	if _, p := Parse(Aristotle, valid); len(p) == 0 {
		t.Fatal("normal answer accepted draft")
	}
	for _, raw := range []string{
 `{"message":"Proposed","draft":{"title":"x","context":null,"deadline":null,"card_id":null},"statements":[]}`,
		`{"message":"Proposed","draft":{"title":"x","context":"","deadline":"2026-02-30","card_id":null},"statements":[]}`,
		`{"message":"Proposed","draft":{"title":"x","context":"","deadline":null,"card_id":"bad"},"statements":[]}`,
		`{"message":"Proposed","draft":{"title":"x","context":"","deadline":null,"card_id":null,"owner_id":"other"},"statements":[]}`,
		`{"message":"Proposed","draft":{"title":"x","context":""},"statements":[]}`,
		`{"statements":[{"kind":"gap","text":"Unknown","cites":[]}]}`,
	} {
		if _, p := Parse(Kind("manual_decision_draft"), []byte(raw)); len(p) == 0 {
			t.Fatalf("invalid draft accepted: %s", raw)
		}
	}
}
