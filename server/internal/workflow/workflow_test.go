package workflow

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

func TestTheBundleHoldsTheRelease(t *testing.T) {
	if Release() != "v0.1.0" || len(All()) != 70 {
		t.Fatalf("release %q, %d workflows", Release(), len(All()))
	}
	w, ok := Get("mendota-ice-phenology")
	if !ok || w.Domain != "earth-sciences" || w.CPUs != 2 {
		t.Fatalf("got %+v, %v", w, ok)
	}
	if SkillName(w.Slug) != "workflow-mendota-ice-phenology" {
		t.Fatal(SkillName(w.Slug))
	}
}

func TestReadFileRefusesWhatIsNotEmbedded(t *testing.T) {
	const slug = "mendota-ice-phenology"
	skipped := 0
	for _, w := range All() {
		for _, f := range w.Files {
			if !f.Embedded {
				skipped++
				if _, err := ReadFile(w.Slug, f.Path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s/%s: %v", w.Slug, f.Path, err)
				}
			}
		}
	}
	if skipped == 0 {
		t.Fatal("no workflow has a non-embedded file")
	}
	for _, c := range [][2]string{{slug, "../index.json"}, {slug, "/etc/passwd"}, {"no-such", "README.md"}, {"../" + slug, "README.md"}} {
		if _, err := ReadFile(c[0], c[1]); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%q, %q) = %v", c[0], c[1], err)
		}
	}
	if b, err := ReadFile(slug, "instruction.md"); err != nil || len(b) == 0 {
		t.Fatalf("instruction.md: %v", err)
	}
}

func TestSkillTarHasNoTests(t *testing.T) {
	for _, w := range All() {
		var buf bytes.Buffer
		if err := SkillTar(&buf, w.Slug); err != nil {
			t.Fatal(w.Slug, err)
		}
		tr, seen := tar.NewReader(&buf), false
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			seen = seen || h.Name == "SKILL.md"
			if slices.Contains(strings.Split(h.Name, "/"), "tests") {
				t.Errorf("%s: %s", w.Slug, h.Name)
			}
		}
		if !seen {
			t.Errorf("%s has no SKILL.md", w.Slug)
		}
	}
	if err := SkillTar(io.Discard, "no-such"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}
