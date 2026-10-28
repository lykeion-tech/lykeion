package doc

import (
	"encoding/json"
	"os"
	"testing"
)

func TestChangeSetMatchesCodeMirror(t *testing.T) {
	b, e := os.ReadFile("testdata/changesets.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Doc     string
		Changes json.RawMessage
		Result  string
		Maps    [][3]int
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	if len(cases) != 500 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		cs, e := Parse(c.Changes)
		if e != nil {
			t.Fatalf("%d: %v", i, e)
		}
		got, e := cs.Apply(c.Doc)
		if e != nil || got != c.Result {
			t.Fatalf("%d apply %q != %q: %v", i, got, c.Result, e)
		}
		for _, m := range c.Maps {
			if got := cs.MapPos(m[0], m[1]); got != m[2] {
				t.Fatalf("%d map %v = %d", i, m, got)
			}
		}
	}
}
func TestChangeSetValidation(t *testing.T) {
	for _, s := range []string{`null`, `{}`, `[-1]`, `[1.5]`, `[[0,null]]`, `[[0,"a\nb"]]`, `[2147483648]`, `[[0,"\ud800"]]`} {
		if _, e := Parse(json.RawMessage(s)); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	c, _ := Parse(json.RawMessage(`[2]`))
	if _, e := c.Apply("a"); e == nil {
		t.Fatal("length mismatch")
	}
	c, _ = Parse(json.RawMessage(`[1,[1]]`))
	if _, e := c.Apply("😀"); e == nil {
		t.Fatal("split surrogate")
	}
}
func TestChangeSetReplaceCompose(t *testing.T) {
	a := Replace(1, 3, "🧪", 4)
	b := Replace(3, 4, "z", 4)
	c, e := a.Compose(b)
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.Apply("a😀b")
	if e != nil || out != "a🧪z" {
		t.Fatalf("%q %v", out, e)
	}
}

func TestChangeSetComposeMatchesCodeMirror(t *testing.T) {
	b, e := os.ReadFile("testdata/changesets.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Doc             string
		Changes, Second json.RawMessage
		ComposedResult  string
		ComposedMaps    [][3]int
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for i, c := range cases {
		a, e := Parse(c.Changes)
		if e != nil {
			t.Fatal(e)
		}
		b, e := Parse(c.Second)
		if e != nil {
			t.Fatal(e)
		}
		result, e := a.Compose(b)
		if e != nil {
			t.Fatalf("%d compose: %v", i, e)
		}
		s, e := result.Apply(c.Doc)
		if e != nil || s != c.ComposedResult {
			t.Fatalf("%d apply %q != %q: %v", i, s, c.ComposedResult, e)
		}
		for _, m := range c.ComposedMaps {
			if got := result.MapPos(m[0], m[1]); got != m[2] {
				t.Fatalf("%d composed map %v = %d", i, m, got)
			}
		}
	}
}

func TestChangeSetRejectsNullLengthsAndNUL(t *testing.T) {
	for _, raw := range []string{`[[null,"x"]]`, `[[0,"\u0000"]]`} {
		if _, e := Parse(json.RawMessage(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
