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
	Sources                    []string
	ScienceIndex               bool
}

// Pack builds a valid pack archive: an author role and a reviewer in each
// column.
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
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Zip builds an archive holding files, keyed by path.
func Zip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write([]byte(body))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
