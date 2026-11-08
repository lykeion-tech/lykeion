package autoresearch

import "testing"

func TestAutoresearchOpenCardRechecksStage(t *testing.T) {
	f := setup(t)
	a := f.stagedAgent(t, "grounder", "grounding")
	ap := f.autoresearch(t, "create_card", "Weekly", "What is new?", "")
	f.exec(t, `UPDATE autoresearch SET assignee_type = 'agent', assignee_id = $1, scope_from = 'grounding', scope_to = 'validation' WHERE id = $2`, a, ap)
	if r := f.fire(t, ap, Admission{}); r.CardID != nil {
		t.Fatalf("a Grounding agent got a card spanning every column: %+v", r)
	}
	if n := f.count(t, `SELECT count(*) FROM card`); n != 0 {
		t.Fatalf("%d cards", n)
	}
}
