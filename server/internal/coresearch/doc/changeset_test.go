package doc

import (
	"encoding/json"
	"os"
	"testing"
	"unicode/utf16"
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

// Compose combines sequential edits without requiring the source text. Its
// section boundaries follow CodeMirror so MapPos retains association semantics.
func (c ChangeSet) Compose(next ChangeSet) (ChangeSet, error) {
	if c.NewLen() != next.Len() {
		return ChangeSet{}, ErrInvalid
	}
	a, b := newSectionCursor(c), newSectionCursor(next)
	type composedSection struct {
		length, inserted int
		text             []uint16
	}
	sections := []composedSection{}
	add := func(length, inserted int, text []uint16, join bool) {
		if length == 0 && inserted <= 0 {
			return
		}
		last := len(sections) - 1
		if last >= 0 && inserted <= 0 && inserted == sections[last].inserted {
			sections[last].length += length
		} else if last >= 0 && length == 0 && sections[last].length == 0 {
			sections[last].inserted += inserted
		} else if join && last >= 0 {
			sections[last].length += length
			sections[last].inserted += inserted
		} else {
			sections = append(sections, composedSection{length: length, inserted: inserted})
		}
		if len(text) > 0 {
			last = len(sections) - 1
			sections[last].text = append(sections[last].text, text...)
		}
	}
	join := false
	for !a.done() || !b.done() {
		if a.inserted == 0 {
			add(a.length, 0, nil, join)
			a.next()
			continue
		}
		if b.length == 0 && !b.done() {
			add(0, b.inserted, b.text, join)
			b.next()
			continue
		}
		if a.done() || b.done() {
			return ChangeSet{}, ErrInvalid
		}
		n := min(a.outputLength(), b.length)
		previous := len(sections)
		if a.inserted < 0 {
			ins := b.inserted
			if ins >= 0 && b.offset > 0 {
				ins = 0
			}
			var text []uint16
			if ins > 0 {
				text = b.text
			}
			add(n, ins, text, join)
		} else if b.inserted < 0 {
			length := a.length
			if a.offset > 0 {
				length = 0
			}
			add(length, n, a.text[a.offset:a.offset+n], join)
		} else {
			length, ins := a.length, b.inserted
			if a.offset > 0 {
				length = 0
			}
			var text []uint16
			if b.offset > 0 {
				ins = 0
			} else {
				text = b.text
			}
			add(length, ins, text, join)
		}
		join = (a.inserted > n || b.inserted >= 0 && b.length > n) && (join || len(sections) > previous)
		a.advanceOutput(n)
		b.advanceInput(n)
	}
	out := ChangeSet{}
	for _, s := range sections {
		if s.inserted < 0 {
			out.sections = append(out.sections, section{length: s.length})
			continue
		}
		if !validUnits(s.text) {
			return ChangeSet{}, ErrInvalid
		}
		text := string(utf16.Decode(s.text))
		out.sections = append(out.sections, section{s.length, &text})
	}
	return out, nil
}
