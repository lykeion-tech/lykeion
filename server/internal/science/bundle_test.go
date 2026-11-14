package science

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestTheBundleIsTheCuratedCopy(t *testing.T) {
	es, err := fs.ReadDir(bundle, "bundle")
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range es {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if !slices.Equal(dirs, Allow) {
		t.Fatalf("bundled %d folders, allowed %d", len(dirs), len(Allow))
	}
	if len(dirs) != 125 || len(Cut) != 52 {
		t.Fatalf("bundled %d, cut %d", len(dirs), len(Cut))
	}
	notice, err := File("NOTICE")
	if err != nil || !strings.Contains(string(notice), Commit) || !strings.Contains(string(notice), "K-Dense") {
		t.Fatalf("NOTICE does not name K-Dense and the pinned commit: %v", err)
	}
	idx := Skills()
	if len(idx) != len(dirs) {
		t.Fatalf("index.json lists %d skills", len(idx))
	}
	for i, name := range dirs {
		b, err := File(name + "/SKILL.md")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fm, _, err := ParseFrontmatter(b)
		if err != nil || fm.Name != name {
			t.Errorf("%s: frontmatter %+v %v", name, fm, err)
			continue
		}
		lic, ok := Licences.Licence(name, fm.License)
		if !ok || idx[i].Name != name || idx[i].Licence != lic || idx[i].Domain == "" || idx[i].Description == "" {
			t.Errorf("%s: licence %q (%v), index %+v", name, lic, ok, idx[i])
		}
		if !strings.Contains(string(notice), "\n  "+name+"  "+lic+"  ") {
			t.Errorf("NOTICE does not list %s under %s", name, lic)
		}
	}
	for name := range Cut {
		if _, err := fs.Stat(bundle, "bundle/"+name); err == nil {
			t.Errorf("cut skill %s is bundled", name)
		}
		if !strings.Contains(string(notice), "\n  "+name+"  ") {
			t.Errorf("NOTICE does not give the reason %s was cut", name)
		}
	}
}

func TestOverlaysComeFirstAndOnlyConnectorReferencesRemain(t *testing.T) {
	for skill, keep := range Keep() {
		b, _ := File(skill + "/SKILL.md")
		_, body, err := ParseFrontmatter(b)
		if err != nil || !bytes.HasPrefix(body, []byte("\n"+Overlays[skill])) {
			t.Errorf("%s: the overlay is not the first section", skill)
		}
		es, err := fs.ReadDir(bundle, "bundle/"+skill+"/references")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range es {
			got = append(got, strings.TrimSuffix(e.Name(), ".md"))
		}
		if want := slices.Sorted(slices.Values(keep)); !slices.Equal(got, want) {
			t.Errorf("%s keeps %v, want %v", skill, got, want)
		}
	}
	for skill, text := range Overlays {
		if !strings.Contains(text, "call_tool") || !strings.Contains(text, "recorded on the study") ||
			strings.Contains(text, "lyk_sources") || strings.Contains(strings.ToLower(text), "gate") {
			t.Errorf("%s: overlay text %q", skill, text)
		}
	}
}

func TestTarHoldsTheWholeSkillFolder(t *testing.T) {
	var buf bytes.Buffer
	if err := Tar(&buf, "paper-lookup"); err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	for _, want := range []string{"SKILL.md", "scripts/_common.py", "references/arxiv.md"} {
		if !slices.Contains(names, want) {
			t.Errorf("the tar lacks %s: %v", want, names)
		}
	}
	if slices.Contains(names, "references/openalex.md") {
		t.Error("a reference the overlay removed is served")
	}
	if err := Tar(io.Discard, "nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an unknown skill: %v", err)
	}
	if got := Closest("scanpi", 3); len(got) != 3 || got[0] != "scanpy" {
		t.Fatalf("closest: %v", got)
	}
}

func TestSectionsSplitOnLevelTwoHeadings(t *testing.T) {
	ss, err := Sections("paper-lookup")
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) < 2 || ss[0].Heading != "In Lykeion: fetch through call_tool" || !strings.Contains(ss[0].Text, "recorded on the study") {
		t.Fatalf("sections: %+v", ss)
	}
	for _, s := range ss {
		if s.Heading == "" || strings.Contains(s.Text, "\n## ") {
			t.Errorf("bad section %q", s.Heading)
		}
	}
	if _, err := Sections("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an unknown skill: %v", err)
	}
}

func TestSplitSections(t *testing.T) {
	got := splitSections("intro\n## A\none\n## B\ntwo\n### sub\n## C\nthree")
	var heads []string
	for _, s := range got {
		heads = append(heads, s.Heading)
	}
	if !slices.Equal(heads, []string{"", "A", "B", "C"}) {
		t.Fatalf("headings %v", heads)
	}
	if !strings.Contains(got[len(got)-2].Text, "### sub") {
		t.Errorf("a level-three heading split a section: %+v", got)
	}
}

func TestASkillWithoutHeadingsIsOneSection(t *testing.T) {
	got := splitSections(strings.Repeat("x", 3000))
	if len(got) != 1 || got[0].Heading != "" || len(got[0].Text) != 2000 {
		t.Fatalf("%d sections, first %d bytes", len(got), len(got[0].Text))
	}
}
