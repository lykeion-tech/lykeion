package testutil

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

// PackOpts describes a field pack for tests. Zero values give a valid pack
// named "demo", version "1", with one Grounding column.
type PackOpts struct {
	Name, Version, Description string
	Columns                    []string // default grounding
	StartKinds                 []string // default question
	Cap                        int32
	Sources                    []string
	ScienceIndex               bool
}

// PackCheck passes when the card has a note record and fails with
// missing_note otherwise.
const PackCheck = `def check(records):
    if [r for r in records if r["kind"] == "note"]:
        return [{"id": "note", "ok": True}]
    return [{"id": "note", "ok": False, "failure": "missing_note"}]
`

// Pack builds a valid pack archive: an author role and a reviewer in each
// column, and PackCheck as each column's check.
func Pack(t *testing.T, o PackOpts) []byte {
	t.Helper()
	if o.Name == "" {
		o.Name = "demo"
	}
	if o.Version == "" {
		o.Version = "1"
	}
	if len(o.Columns) == 0 {
		o.Columns = []string{"grounding"}
	}
	if len(o.StartKinds) == 0 {
		o.StartKinds = []string{"question"}
	}
	cols := map[string]any{}
	for _, c := range o.Columns {
		cols[c] = map[string]any{"phases": [][]string{{"author"}}, "reviewer": "reviewer"}
	}
	m := map[string]any{"name": o.Name, "version": o.Version, "description": o.Description,
		"start_kinds": o.StartKinds, "columns": cols, "failures": map[string]string{"missing_note": o.Columns[0]}}
	if o.Cap != 0 {
		m["cap"] = o.Cap
	}
	if len(o.Sources) > 0 {
		m["sources"] = o.Sources
	}
	if o.ScienceIndex {
		m["science_index"] = true
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, body []byte) {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write(body)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	add("pack.json", manifest)
	for _, c := range o.Columns {
		add("skills/"+c+"/author/SKILL.md", []byte("Do the "+c+" work."))
		add("skills/"+c+"/reviewer/SKILL.md", []byte("Review the "+c+" work."))
		add("checks/"+c+".star", []byte(PackCheck))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
