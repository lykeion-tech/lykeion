package collaboration

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func apply(t *testing.T, d *Document, e Edit) {
	t.Helper()
	if _, err := d.Apply(e); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentUnicodeInsertDeleteAndReplay(t *testing.T) {
	edits := []Edit{{ID: "alice", Actor: "u_a", Block: "p", Start: 1, Text: "🧪"}, {ID: "bob", Actor: "u_b", Block: "p", Start: 1, Delete: 1}, {ID: "agent", Actor: "a_1", Block: "p", Start: 1, Text: "λ"}}
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}, {0, 2, 1}, {1, 2, 0}, {2, 0, 1}} {
		d := New(map[string]string{"p": "αβγ"})
		for _, i := range order {
			apply(t, d, edits[i])
		}
		if d.Text("p") != "α🧪λγ" {
			t.Fatal(d.Text("p"))
		}
		r, e := d.Apply(edits[order[0]])
		if e != nil || r != 1 {
			t.Fatal("duplicate not idempotent")
		}
	}
}
func TestOwnUndoPreservesOtherEdits(t *testing.T) {
	d := New(map[string]string{"p": "ab"})
	apply(t, d, Edit{ID: "a", Actor: "alice", Block: "p", Start: 1, Text: "X"})
	apply(t, d, Edit{ID: "b", Actor: "bob", Block: "p", Base: 1, Start: 2, Text: "Y"})
	if _, e := d.Undo("a", "bob", "bad"); e == nil {
		t.Fatal("cross-author undo")
	}
	if _, e := d.Undo("a", "alice", "good"); e != nil {
		t.Fatal(e)
	}
	if d.Text("p") != "aYb" {
		t.Fatal(d.Text("p"))
	}
	if r, e := d.Undo("a", "alice", "good"); e != nil || r != 3 {
		t.Fatal("undo replay")
	}
}
func TestOverlappingDeletesUndoAndFormatting(t *testing.T) {
	d := New(map[string]string{"p": "abc"})
	apply(t, d, Edit{ID: "a", Actor: "alice", Block: "p", Start: 1, Delete: 1})
	apply(t, d, Edit{ID: "b", Actor: "bob", Block: "p", Start: 1, Delete: 1})
	d.Undo("a", "alice", "u")
	if d.Text("p") != "ac" {
		t.Fatal("undo revived another deletion")
	}
	apply(t, d, Edit{ID: "fmt", Actor: "bob", Block: "p", Base: 3, Start: 0, Delete: 2, Format: "bold"})
	if !reflect.DeepEqual(d.Formats("p"), [][]string{{"bold"}, {"bold"}}) {
		t.Fatal(d.Formats("p"))
	}
	d.Undo("fmt", "bob", "uf")
	if !reflect.DeepEqual(d.Formats("p"), [][]string{{}, {}}) {
		t.Fatal("format undo")
	}
}
func TestConflictsCompactionSeal(t *testing.T) {
	d := New(map[string]string{"p": "a"})
	e := Edit{ID: "op", Actor: "u", Block: "p", Text: "x"}
	apply(t, d, e)
	e.Text = "other"
	if _, err := d.Apply(e); err == nil {
		t.Fatal("id collision")
	}
	d.CompactBefore(1)
	e.ID = "new"
	if _, err := d.Apply(e); !errors.Is(err, ErrRebase) {
		t.Fatal(err)
	}
	d.Seal()
	e.Base = 1
	if _, err := d.Apply(e); err == nil {
		t.Fatal("sealed edit")
	}
}
func TestConcurrentGoroutines(t *testing.T) {
	d := New(map[string]string{"p": "a"})
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c"} {
		wg.Go(func() {
			_, err := d.Apply(Edit{ID: id, Actor: id, Block: "p", Text: id})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if d.Text("p") != "cbaa" {
		t.Fatal(d.Text("p"))
	}
}

func TestCausalInsertionPrecedesExistingChild(t *testing.T) {
	d := New(map[string]string{"p": "a"})
	apply(t, d, Edit{ID: "z", Actor: "u", Block: "p", Text: "Z"})
	apply(t, d, Edit{ID: "a", Actor: "u", Block: "p", Base: 1, Text: "A"})
	if d.Text("p") != "AZa" {
		t.Fatal(d.Text("p"))
	}
}
