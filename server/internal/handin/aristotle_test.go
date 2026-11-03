package handin

import (
	"strings"
	"testing"
)

func TestKindForAristotle(t *testing.T) {
	if k, ok := KindFor("aristotle", "aristotle", ""); !ok || k != Aristotle {
		t.Fatal(k, ok)
	}
}

func TestAristotleSchemaValidates(t *testing.T) {
	valid := `{"statements":[{"kind":"gap","text":"No record.","cites":[]}]}`
	h, probs := Parse(Aristotle, []byte(valid))
	if len(probs) > 0 || len(h.Statements) != 1 {
		t.Fatal(h, probs)
	}
	const gap = `{"kind":"gap","text":"x","cites":[]}`
	for _, bad := range []string{
		`{}`, `{"statements":[]}`,
		`{"statements":[` + strings.Repeat(gap+",", 40) + gap + `]}`,
		strings.Replace(valid, "gap", "claim", 1),
		strings.Replace(valid, "No record.", strings.Repeat("x", 2001), 1),
		strings.Replace(valid, `,"cites":[]`, "", 1),
		strings.Replace(valid, `"cites":[]`, `"cites":[null]`, 1),
		strings.Replace(valid, "No record.", "", 1),
		strings.Replace(valid, `"kind"`, `"extra":true,"kind"`, 1),
	} {
		if _, p := Parse(Aristotle, []byte(bad)); len(p) == 0 {
			t.Fatal("accepted invalid Aristotle statement")
		}
	}
}
