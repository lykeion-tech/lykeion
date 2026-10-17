package testutil

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

// PackOpts describes a Team for tests. Zero values give a valid Team named
// "demo", version "1", with a leader "pi" and one member.
type PackOpts struct {
	Name, Version, Description string
	Domain, Stage              string
	Members                    int // default 1
	Criteria                   []string
	Steps                      int
	Catalogue                  map[string][]string         // member name → catalogue connectors it declares
	Connectors                 map[string][]map[string]any // member name → further connector entries, as pack.json holds them
}

// Pack builds a valid Team archive: every agent uses harness claude except the
// auditor, which uses codex. The leader and each member carry one skill folder.
func Pack(t *testing.T, o PackOpts) []byte {
	t.Helper()
	if o.Name == "" {
		o.Name = "demo"
	}
	if o.Version == "" {
		o.Version = "1"
	}
	if o.Members == 0 {
		o.Members = 1
	}
	conns := func(name string) []map[string]any {
		out := []map[string]any{}
		for _, c := range o.Catalogue[name] {
			out = append(out, map[string]any{"name": c, "catalogue": c})
		}
		return append(out, o.Connectors[name]...)
	}
	member := func(name string) map[string]any {
		return map[string]any{"name": name, "role": "Does the " + name + " work.", "skills": []string{"work"}, "connectors": conns(name),
			"agent": map[string]any{"harness": "claude", "model": "", "instructions": "You are " + name + "."}}
	}
	auditor := map[string]any{"name": "auditor", "role": "Audits the work.", "connectors": conns("auditor"),
		"agent": map[string]any{"harness": "codex", "model": "", "instructions": "You audit."}}
	files := map[string]string{"skills/pi/work/SKILL.md": "Lead the work."}
	members := []any{}
	for i := 1; i <= o.Members; i++ {
		name := fmt.Sprintf("member-%d", i)
		members = append(members, member(name))
		files["skills/"+name+"/work/SKILL.md"] = "Do the work."
	}
	if o.Criteria == nil {
		o.Criteria = []string{}
	}
	manifest, err := json.Marshal(map[string]any{"name": o.Name, "version": o.Version, "description": o.Description,
		"domain": o.Domain, "stage": o.Stage, "leader": member("pi"), "auditor": auditor, "members": members,
		"criteria": o.Criteria, "steps": o.Steps})
	if err != nil {
		t.Fatal(err)
	}
	files["pack.json"] = string(manifest)
	return Zip(t, files)
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
