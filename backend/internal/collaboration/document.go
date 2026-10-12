// Package collaboration contains the bounded server-authoritative draft editing spike.
// Unicode code-point positions resolve to stable atoms in the submitted base revision.
package collaboration

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

var ErrRebase = errors.New("DOCUMENT_REBASE_REQUIRED")

type Edit struct {
	ID     string `json:"id"`
	Actor  string `json:"actor"`
	Block  string `json:"block"`
	Base   uint64 `json:"base"`
	Start  int    `json:"start"`
	Delete int    `json:"delete"`
	Text   string `json:"text"`
	Format string `json:"format,omitempty"`
}
type atom struct {
	ID, Parent, Text string
	Deleted          map[string]bool
	Formats          map[string]string
}
type receipt struct {
	Raw                          string
	Revision                     uint64
	Inserted, Deleted, Formatted []string
	Actor, Block                 string
	Undone                       bool
}
type Document struct {
	mu        sync.Mutex
	revision  uint64
	sealed    bool
	blocks    map[string]map[string]*atom
	snapshots map[uint64]map[string][]string
	receipts  map[string]*receipt
}

func New(blocks map[string]string) *Document {
	d := &Document{blocks: map[string]map[string]*atom{}, snapshots: map[uint64]map[string][]string{}, receipts: map[string]*receipt{}}
	for key, text := range blocks {
		atoms := map[string]*atom{}
		parent := ""
		for i, r := range []rune(text) {
			id := fmt.Sprintf("0:%09d", i)
			atoms[id] = &atom{id, parent, string(r), map[string]bool{}, map[string]string{}}
			parent = id
		}
		d.blocks[key] = atoms
	}
	d.snapshot()
	return d
}
func (d *Document) visible(block string) []string {
	nodes := d.blocks[block]
	children := map[string][]string{}
	for id, a := range nodes {
		children[a.Parent] = append(children[a.Parent], id)
	}
	for _, ids := range children {
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	}
	out := []string{}
	var visit func(string)
	visit = func(parent string) {
		for _, id := range children[parent] {
			if len(nodes[id].Deleted) == 0 {
				out = append(out, id)
			}
			visit(id)
		}
	}
	visit("")
	return out
}
func (d *Document) snapshot() {
	snap := map[string][]string{}
	for block := range d.blocks {
		snap[block] = d.visible(block)
	}
	d.snapshots[d.revision] = snap
}
func (d *Document) Apply(e Edit) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, _ := json.Marshal(e)
	if old, ok := d.receipts[e.ID]; ok {
		if old.Raw != string(raw) {
			return 0, errors.New("IDEMPOTENCY_MISMATCH")
		}
		return old.Revision, nil
	}
	if d.sealed {
		return 0, errors.New("SEALED_DOCUMENT")
	}
	if e.ID == "" || e.Actor == "" || len(e.ID) > 128 || strings.Contains(e.ID, ":") || !utf8.ValidString(e.Text) || len([]rune(e.Text)) > 10000 {
		return 0, errors.New("invalid edit")
	}
	if e.Format != "" && e.Format != "bold" && e.Format != "italic" {
		return 0, errors.New("unsupported format")
	}
	snap, ok := d.snapshots[e.Base]
	if !ok {
		return 0, ErrRebase
	}
	base, ok := snap[e.Block]
	if !ok {
		return 0, errors.New("unknown block")
	}
	if e.Start < 0 || e.Delete < 0 || e.Start > len(base) || e.Delete > len(base)-e.Start {
		return 0, errors.New("invalid range")
	}
	if e.Format != "" && e.Text != "" {
		return 0, errors.New("format and splice are distinct")
	}
	r := &receipt{Raw: string(raw), Actor: e.Actor, Block: e.Block}
	nodes := d.blocks[e.Block]
	for _, id := range base[e.Start : e.Start+e.Delete] {
		if e.Format != "" {
			nodes[id].Formats[e.ID] = e.Format
			r.Formatted = append(r.Formatted, id)
		} else {
			nodes[id].Deleted[e.ID] = true
			r.Deleted = append(r.Deleted, id)
		}
	}
	parent := ""
	if e.Start > 0 {
		parent = base[e.Start-1]
	}
	for i, ch := range []rune(e.Text) {
		id := fmt.Sprintf("1:%020d:%s:%09d", e.Base+1, e.ID, i)
		nodes[id] = &atom{id, parent, string(ch), map[string]bool{}, map[string]string{}}
		r.Inserted = append(r.Inserted, id)
		parent = id
	}
	d.revision++
	r.Revision = d.revision
	d.receipts[e.ID] = r
	d.snapshot()
	return d.revision, nil
}
func (d *Document) Undo(id, actor, undoID string) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw := fmt.Sprintf("undo:%s:%s", id, actor)
	if old, ok := d.receipts[undoID]; ok {
		if old.Raw != raw {
			return 0, errors.New("IDEMPOTENCY_MISMATCH")
		}
		return old.Revision, nil
	}
	if d.sealed {
		return 0, errors.New("SEALED_DOCUMENT")
	}
	r, ok := d.receipts[id]
	if !ok || r.Actor != actor || r.Undone || undoID == "" {
		return 0, errors.New("undo forbidden")
	}
	nodes := d.blocks[r.Block]
	for _, x := range r.Inserted {
		nodes[x].Deleted[undoID] = true
	}
	for _, x := range r.Deleted {
		delete(nodes[x].Deleted, id)
	}
	for _, x := range r.Formatted {
		delete(nodes[x].Formats, id)
	}
	r.Undone = true
	d.revision++
	d.receipts[undoID] = &receipt{Raw: raw, Revision: d.revision, Actor: actor, Undone: true}
	d.snapshot()
	return d.revision, nil
}
func (d *Document) Text(block string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var b strings.Builder
	for _, id := range d.visible(block) {
		b.WriteString(d.blocks[block][id].Text)
	}
	return b.String()
}
func (d *Document) Formats(block string) [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := [][]string{}
	for _, id := range d.visible(block) {
		set := map[string]bool{}
		for _, v := range d.blocks[block][id].Formats {
			set[v] = true
		}
		v := []string{}
		for k := range set {
			v = append(v, k)
		}
		sort.Strings(v)
		out = append(out, v)
	}
	return out
}
func (d *Document) Seal() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sealed = true
	return d.revision
}
func (d *Document) CompactBefore(rev uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for v := range d.snapshots {
		if v < rev && v < d.revision {
			delete(d.snapshots, v)
		}
	}
}
