package pack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lykeion/server/internal/testutil"
)

func member(name string) Member {
	return Member{Name: name, Role: "role of " + name, Agent: AgentDef{Harness: "claude", Instructions: "i"}}
}

func goodManifest() Manifest {
	lead, scout := member("pi"), member("scout")
	lead.Skills = []string{"lead"}
	scout.Skills = []string{"search", "statistical-analysis"}
	scout.Connectors = []Connector{{Name: "ncbi", Catalogue: "ensembl"}, {Name: "docs", URL: "https://x.org/mcp"},
		{Name: "local", Command: []string{"npx", "-y", "pkg"}, Env: map[string]string{"MODE": "fast"}}}
	return Manifest{Name: "bio", Version: "1", Description: "d", Domain: "biology", Stage: "grounding",
		Leader: lead, Auditor: Member{Name: "auditor", Role: "audits", Agent: AgentDef{Harness: "codex", Model: "m"}},
		Members: []Member{scout, member("analyst")}, Criteria: []string{"Is it clear?"}, Steps: 20}
}

func goodSkills() map[string]map[string][]byte {
	return map[string]map[string][]byte{
		"pi/lead":      {"SKILL.md": []byte("lead skill")},
		"scout/search": {"SKILL.md": []byte("search skill"), "ref.md": []byte("ref")},
	}
}

func goodZip(t *testing.T, edit func(m *Manifest)) []byte {
	t.Helper()
	m := goodManifest()
	if edit != nil {
		edit(&m)
	}
	z, err := Zip(m, goodSkills())
	if err != nil {
		t.Fatal(err)
	}
	return z
}

type entry struct {
	name string
	body []byte
	mode uint32 // 0 means a regular file
}

func build(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(modeOf(e.mode))
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func bomb(good []entry) []entry {
	out := append([]entry{}, good...)
	for i := 0; i < maxExpanded/MaxFile+1; i++ {
		out = append(out, entry{name: fmt.Sprintf("skills/grounding/scout/b%d.bin", i), body: make([]byte, MaxFile)})
	}
	return out
}

func rawManifest(good []entry, f func([]byte) []byte) []entry {
	out := append([]entry{}, good...)
	out[0].body = f(append([]byte{}, out[0].body...))
	return out
}

func baseEntries(t *testing.T) []entry {
	b, err := ManifestJSON(goodManifest())
	if err != nil {
		t.Fatal(err)
	}
	return []entry{
		{name: "pack.json", body: b},
		{name: "skills/pi/lead/SKILL.md", body: []byte("lead skill")},
		{name: "skills/scout/search/SKILL.md", body: []byte("search skill")},
		{name: "skills/scout/search/ref.md", body: []byte("ref")},
	}
}

func TestParseReadsTheTemplate(t *testing.T) {
	z := goodZip(t, nil)
	p, err := Parse(z)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Manifest, goodManifest()) {
		t.Fatalf("%+v", p.Manifest)
	}
	if !reflect.DeepEqual(p.Skills, goodSkills()) || string(p.SkillFiles("scout", "search")["ref.md"]) != "ref" {
		t.Fatalf("%+v", p.Skills)
	}
	if sum := sha256.Sum256(z); p.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("sha256")
	}
}

func TestStepsDefaultToTwelve(t *testing.T) {
	p, err := Parse(goodZip(t, func(m *Manifest) { m.Steps = 0 }))
	if err != nil || p.Manifest.Steps != 12 {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestToleranceIsRead(t *testing.T) {
	p, err := Parse(goodZip(t, func(m *Manifest) { m.Validation.Tolerance = 0.5 }))
	if err != nil || p.Manifest.Validation.Tolerance != 0.5 {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestParseRefuses(t *testing.T) {
	good := baseEntries(t)
	ext := func(e entry) []entry { return append(append([]entry{}, good...), e) }
	many := append([]entry{}, good...)
	for i := 0; i < MaxFiles; i++ {
		many = append(many, entry{name: fmt.Sprintf("skills/scout/search/f%d.md", i), body: []byte("x")})
	}
	mut := func(f func(m *Manifest)) []byte { return goodZip(t, f) }
	rawJSON := func(f func(b []byte) []byte) []byte { return build(t, rawManifest(good, f)...) }
	tooMany := func() (out []Member) {
		for i := 0; i < MaxMembers+1; i++ {
			out = append(out, member(fmt.Sprintf("m%d", i)))
		}
		return
	}
	cases := []struct {
		name string
		z    []byte
		want string // a fragment the error names
	}{
		{"dotdot", build(t, ext(entry{name: "skills/../x", body: []byte("x")})...), "unsafe path"},
		{"absolute", build(t, ext(entry{name: "/abs", body: []byte("x")})...), "unsafe path"},
		{"symlink", build(t, ext(entry{name: "skills/scout/search/l", body: []byte("SKILL.md"), mode: modeSymlink})...), "regular file"},
		{"too big", append(bytes.Repeat([]byte{0}, MaxArchive), build(t, good...)...), "exceeds"},
		{"too many", build(t, many...), "entries"},
		{"zip bomb", build(t, bomb(good)...), "exceed"},
		{"oversize file", build(t, ext(entry{name: "skills/scout/search/big.bin", body: make([]byte, MaxFile+1)})...), "exceeds"},
		{"case dup file", build(t, ext(entry{name: "skills/scout/search/skill.md", body: []byte("x")})...), "duplicate"},
		{"stray", build(t, ext(entry{name: "evil.sh", body: []byte("x")})...), "unexpected file"},
		{"skills for unknown member", build(t, ext(entry{name: "skills/ghost/s/SKILL.md", body: []byte("x")})...), "unknown member"},
		{"trailing object", rawJSON(func(b []byte) []byte { return append(b, " {}"...) }), "trailing"},
		{"duplicate key", rawJSON(func(b []byte) []byte { return append([]byte(`{"name":"a",`), b[1:]...) }), "duplicate key"},
		{"case-variant key", rawJSON(func(b []byte) []byte { return append([]byte(`{"NAME":"a",`), b[1:]...) }), "NAME"},
		{"nested case-variant key", rawJSON(func(b []byte) []byte { return bytes.Replace(b, []byte(`"harness"`), []byte(`"Harness"`), 1) }), "Harness"},
		{"unknown nested field", rawJSON(func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"harness"`), []byte(`"surprise": 1, "harness"`), 1)
		}), "surprise"},
		{"unknown field", rawJSON(func(b []byte) []byte { return append([]byte(`{"surprise":1,`), b[1:]...) }), "surprise"},
		{"empty name", mut(func(m *Manifest) { m.Name = "" }), "name"},
		{"stage not a stage", mut(func(m *Manifest) { m.Stage = "lunch" }), "stage"},
		{"stage reporting", mut(func(m *Manifest) { m.Stage = "reporting" }), "stage"},
		{"no members", mut(func(m *Manifest) { m.Members = nil }), "members"},
		{"too many members", mut(func(m *Manifest) { m.Members = tooMany() }), "members"},
		{"bad member name", mut(func(m *Manifest) { m.Members[0].Name = "Scout" }), "name"},
		{"repeated member name", mut(func(m *Manifest) { m.Members[1].Name = "scout" }), "repeated"},
		{"leader shares a member's name", mut(func(m *Manifest) { m.Leader.Name = "scout" }), "repeated"},
		{"too many criteria", mut(func(m *Manifest) { m.Criteria = make([]string, MaxCriteria+1) }), "criteria"},
		{"long criterion", mut(func(m *Manifest) { m.Criteria = []string{strings.Repeat("x", MaxCriterionLen+1)} }), "criterion"},
		{"steps too high", mut(func(m *Manifest) { m.Steps = MaxSteps + 1 }), "steps"},
		{"steps negative", mut(func(m *Manifest) { m.Steps = -1 }), "steps"},
		{"tolerance too wide", mut(func(m *Manifest) { m.Validation.Tolerance = 0.51 }), "tolerance"},
		{"tolerance negative", mut(func(m *Manifest) { m.Validation.Tolerance = -0.01 }), "tolerance"},
		{"unknown harness", mut(func(m *Manifest) { m.Members[0].Agent.Harness = "gpt" }), "harness"},
		{"unknown skill", mut(func(m *Manifest) { m.Members[0].Skills = []string{"ghost"} }), "skill"},
		{"unknown catalogue", mut(func(m *Manifest) { m.Members[0].Connectors = []Connector{{Name: "x", Catalogue: "nope"}} }), "catalogue"},
		{"connector named sources", mut(func(m *Manifest) { m.Members[0].Connectors[1].Name = "sources" }), "reserved"},
		{"connector named attachments", mut(func(m *Manifest) { m.Members[0].Connectors[1].Name = "Attachments" }), `the connector name "Attachments" is reserved`},
		{"repeated catalogue entry", mut(func(m *Manifest) {
			m.Members[0].Connectors = []Connector{{Catalogue: "ensembl"}, {Catalogue: "ensembl"}}
		}), "repeated"},
		{"repeated connector name", mut(func(m *Manifest) { m.Members[0].Connectors[1].Name = "ncbi" }), "repeated"},
		{"skill declared twice", mut(func(m *Manifest) { m.Members[0].Skills = []string{"search", "search"} }), "twice"},
		{"not a zip", []byte("hello"), "zip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.z)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error naming %q, got %v", c.want, err)
			}
		})
	}
}

func TestAConnectorIsExactlyOneKind(t *testing.T) {
	for name, c := range map[string]struct {
		c  Connector
		ok bool
	}{
		"two kinds":         {Connector{Catalogue: "ensembl", URL: "https://x"}, false},
		"none":              {Connector{}, false},
		"path command":      {Connector{Command: []string{"/bin/sh"}}, false},
		"dotdot command":    {Connector{Command: []string{"..x"}}, false},
		"http url":          {Connector{URL: "http://x.org"}, false},
		"npx":               {Connector{Command: []string{"npx", "-y", "pkg"}}, true},
		"https url":         {Connector{URL: "https://x.org/mcp"}, true},
		"catalogue":         {Connector{Catalogue: "ensembl"}, true},
		"unknown catalogue": {Connector{Catalogue: "openalex"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.c.validate(); (err == nil) != c.ok {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
		})
	}
}

func TestSecretsAreNotEnv(t *testing.T) {
	for _, k := range []string{"API_KEY", "token", "MY_Secret", "PASSWORD"} {
		if err := (Connector{Command: []string{"x"}, Env: map[string]string{k: "v"}}).validate(); err == nil {
			t.Fatalf("%s accepted", k)
		}
	}
}

func TestTestutilPackIsValid(t *testing.T) {
	p, err := Parse(testutil.Pack(t, testutil.PackOpts{Name: "x", Version: "3", Stage: "grounding", Members: 3, Criteria: []string{"c"}}))
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest
	if m.Name != "x" || m.Version != "3" || m.Leader.Name != "pi" || len(m.Members) != 3 || m.Auditor.Agent.Harness != "codex" || m.Steps != 12 {
		t.Fatalf("%+v", m)
	}
}

func TestTheDemoFixtureParses(t *testing.T) {
	root := "../../../tests/e2e/fixtures/demo-pack"
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = string(body)
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("reading the fixture: %v (%d files)", err, len(files))
	}
	p, err := Parse(testutil.Zip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Name != "ui-demo" || len(p.Manifest.Members) != 2 {
		t.Fatalf("manifest %+v", p.Manifest)
	}
}

func TestAuditorCanShareLeaderAndMemberModel(t *testing.T) {
	_, err := Parse(goodZip(t, func(m *Manifest) {
		m.Auditor.Agent = m.Leader.Agent
		m.Members[0].Agent = m.Leader.Agent
	}))
	if err != nil {
		t.Fatalf("same-model auditor should be accepted: %v", err)
	}
}

func TestWorkspaceMCPReferenceIsExclusiveAndConfigurationFree(t *testing.T) {
	id := "12345678-1234-1234-1234-123456789012"
	if err := ValidateConnector(Connector{Name: "lab", Workspace: id}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Connector{
		{Workspace: "invalid"}, {Workspace: "00000000-0000-0000-0000-000000000000"},
		{Workspace: id, URL: "https://example.org"}, {Workspace: id, Command: []string{"echo"}},
		{Workspace: id, Catalogue: "uniprot"}, {Workspace: id, Env: map[string]string{"MODE": "value"}},
	} {
		if err := ValidateConnector(c); err == nil {
			t.Fatalf("invalid workspace reference accepted: %+v", c)
		}
	}
}

func TestModelsMapReplacesHarness(t *testing.T) {
	models := map[string]string{"claude": "claude-opus-5-5", "codex": "gpt-6"}
	if _, err := Parse(goodZip(t, func(m *Manifest) {
		m.Members[0].Agent = AgentDef{Models: models, Instructions: "i"}
	})); err != nil {
		t.Fatalf("a models map was refused: %v", err)
	}
	for name, def := range map[string]AgentDef{
		"both":            {Harness: "claude", Models: models},
		"unknown harness": {Models: map[string]string{"cursor": "x"}},
	} {
		if _, err := Parse(goodZip(t, func(m *Manifest) { m.Members[0].Agent = def })); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
