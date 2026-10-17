package pack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const goodStar = `def check(records):
    return [{"id": r["key"], "ok": False, "failure": "bad"} for r in records]
`

func goodManifest() map[string]any {
	return map[string]any{
		"name": "bio", "version": "1", "description": "d",
		"start_kinds": []string{"question"},
		"columns": map[string]any{
			"grounding": map[string]any{
				"phases":   [][]string{{"scout"}},
				"reviewer": "critic",
				"kinds":    map[string]any{"question": map[string]any{"roles": []string{"scout"}}},
				"config":   map[string]any{"anything": []int{1, 2}},
			},
		},
		"failures": map[string]string{"bad": "grounding"},
	}
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

func baseEntries(t *testing.T, m map[string]any) []entry {
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return []entry{
		{name: "pack.json", body: b},
		{name: "skills/grounding/scout/SKILL.md", body: []byte("scout skill")},
		{name: "skills/grounding/scout/ref.md", body: []byte("ref")},
		{name: "skills/grounding/critic/SKILL.md", body: []byte("critic skill")},
		{name: "checks/grounding.star", body: []byte(goodStar)},
	}
}

func TestParseValid(t *testing.T) {
	z := build(t, baseEntries(t, goodManifest())...)
	p, err := Parse(z)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(z)
	if p.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha %s", p.SHA256)
	}
	if p.Manifest.Name != "bio" || p.Skill("grounding", "scout") != "scout skill" {
		t.Fatalf("%+v", p.Manifest)
	}
	if f := p.SkillFiles("grounding", "scout"); len(f) != 2 || string(f["ref.md"]) != "ref" {
		t.Fatalf("files %v", f)
	}
	if string(p.Manifest.Columns["grounding"].Config) != `{"anything":[1,2]}` {
		t.Fatalf("config %s", p.Manifest.Columns["grounding"].Config)
	}
	if p.Target("bad") != "grounding" {
		t.Fatal("target")
	}
}

func TestCheckRunsPackScript(t *testing.T) {
	p, err := Parse(build(t, baseEntries(t, goodManifest())...))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := p.Check(t.Context(), "grounding", []checkRecord{{Key: "k1"}})
	if err != nil || len(rs) != 1 || rs[0].OK || rs[0].Failure != "bad" {
		t.Fatalf("%v %v", rs, err)
	}
	if _, err := p.Check(t.Context(), "validation", nil); err == nil {
		t.Fatal("a column without a check script must error")
	}
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

func TestParseRefuses(t *testing.T) {
	mut := func(f func(m map[string]any)) []entry {
		m := goodManifest()
		f(m)
		return baseEntries(t, m)
	}
	col := func(m map[string]any) map[string]any {
		return m["columns"].(map[string]any)["grounding"].(map[string]any)
	}
	many := baseEntries(t, goodManifest())
	for i := 0; i < MaxFiles; i++ {
		many = append(many, entry{name: fmt.Sprintf("skills/grounding/scout/f%d.md", i), body: []byte("x")})
	}
	good := baseEntries(t, goodManifest())
	ext := func(e entry) []entry { return append(append([]entry{}, good...), e) }
	cases := map[string][]byte{
		"dotdot":                  build(t, ext(entry{name: "skills/../x", body: []byte("x")})...),
		"dotdot2":                 build(t, ext(entry{name: "../x", body: []byte("x")})...),
		"absolute":                build(t, ext(entry{name: "/abs", body: []byte("x")})...),
		"symlink":                 build(t, ext(entry{name: "skills/grounding/scout/l", body: []byte("SKILL.md"), mode: modeSymlink})...),
		"too big":                 append(bytes.Repeat([]byte{0}, MaxArchive), build(t, good...)...),
		"too many":                build(t, many...),
		"dup pack":                build(t, ext(entry{name: "pack.json", body: good[0].body})...),
		"zip bomb":                build(t, bomb(good)...),
		"oversize file":           build(t, ext(entry{name: "skills/grounding/scout/big.bin", body: make([]byte, MaxFile+1)})...),
		"case dup file":           build(t, ext(entry{name: "skills/grounding/scout/skill.md", body: []byte("x")})...),
		"case dup dir":            build(t, ext(entry{name: "skills/grounding/Scout/SKILL.md", body: []byte("x")})...),
		"undeclared column skill": build(t, ext(entry{name: "skills/reporting/r/SKILL.md", body: []byte("x")})...),
		"undeclared column check": build(t, ext(entry{name: "checks/reporting.star", body: []byte(goodStar)})...),
		"empty name":              build(t, mut(func(m map[string]any) { m["name"] = "" })...),
		"empty version":           build(t, mut(func(m map[string]any) { m["version"] = "" })...),
		"trailing brace":          build(t, rawManifest(good, func(b []byte) []byte { return append(b, " }"...) })...),
		"trailing bracket":        build(t, rawManifest(good, func(b []byte) []byte { return append(b, " ]"...) })...),
		"trailing object":         build(t, rawManifest(good, func(b []byte) []byte { return append(b, " {}"...) })...),
		"duplicate key": build(t, rawManifest(good, func(b []byte) []byte {
			return append([]byte(`{"name":"a",`), b[1:]...)
		})...),
		"case-variant key": build(t, rawManifest(good, func(b []byte) []byte {
			return append([]byte(`{"NAME":"a",`), b[1:]...)
		})...),
		"column case-variant key": build(t, rawManifest(good, func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"reviewer"`), []byte(`"Reviewer"`), 1)
		})...),
		"stray": build(t, ext(entry{name: "evil.sh", body: []byte("x")})...),
		"unknown column": build(t, mut(func(m map[string]any) {
			m["columns"].(map[string]any)["lunch"] = map[string]any{}
		})...),
		"failure outside stages": build(t, mut(func(m map[string]any) { m["failures"] = map[string]string{"bad": "lunch"} })...),
		"empty failure type":     build(t, mut(func(m map[string]any) { m["failures"] = map[string]string{"": "grounding"} })...),
		"empty start_kinds":      build(t, mut(func(m map[string]any) { m["start_kinds"] = []string{} })...),
		"bad start kind":         build(t, mut(func(m map[string]any) { m["start_kinds"] = []string{"idea"} })...),
		"role without skill": build(t, mut(func(m map[string]any) {
			col(m)["phases"] = [][]string{{"scout", "ghost"}}
		})...),
		"kind role outside column": build(t, mut(func(m map[string]any) {
			col(m)["kinds"] = map[string]any{"question": map[string]any{"roles": []string{"ghost"}}}
		})...),
		"bands inverted": build(t, mut(func(m map[string]any) { m["bands"] = map[string]float64{"yes": .3, "no": .5} })...),
		"negative cap":   build(t, mut(func(m map[string]any) { m["cap"] = -1 })...),
		"bad split":      build(t, mut(func(m map[string]any) { m["split"] = map[string]any{"unit": "paper", "ratio": 1.0} })...),
		"unknown field":  build(t, mut(func(m map[string]any) { m["surprise"] = 1 })...),
		"vendored wrong hash": build(t, mut(func(m map[string]any) {
			m["vendored"] = map[string]string{"skills/grounding/scout/ref.md": strings.Repeat("0", 64)}
		})...),
		"vendored missing": build(t, mut(func(m map[string]any) {
			m["vendored"] = map[string]string{"skills/grounding/scout/nope.md": strings.Repeat("0", 64)}
		})...),
		"not a zip": []byte("hello"),
	}
	for name, z := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(z); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestVendoredMatchingHashParses(t *testing.T) {
	sum := sha256.Sum256([]byte("ref"))
	m := goodManifest()
	m["vendored"] = map[string]string{"skills/grounding/scout/ref.md": hex.EncodeToString(sum[:])}
	if _, err := Parse(build(t, baseEntries(t, m)...)); err != nil {
		t.Fatal(err)
	}
}

func TestTargetUndeclared(t *testing.T) {
	p, err := Parse(build(t, baseEntries(t, goodManifest())...))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Target("nope"); got != "" {
		t.Fatalf("%q", got)
	}
}
