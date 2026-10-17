package science

import (
	"slices"
	"strings"
	"testing"
)

func TestEveryBundledSkillHasACheckedLicenceRow(t *testing.T) {
	for _, name := range Allow {
		row, ok := Licences[name]
		if !ok || row.Source == "" || !slices.Contains(Allowed, row.Licence) {
			t.Errorf("%s: no checked licence row: %+v", name, row)
		}
	}
	tbl := LicenceTable{"x": {Licence: "MIT", Stated: "MIT", Source: "skills/x/LICENSE"}}
	if n, ok := tbl.Licence("x", "MIT"); !ok || n != "MIT" {
		t.Errorf("a matching row: %q %v", n, ok)
	}
	for _, c := range []struct{ skill, stated string }{{"x", ""}, {"x", "BSD-3-Clause"}, {"y", "MIT"}, {"y", ""}} {
		if n, ok := tbl.Licence(c.skill, c.stated); ok {
			t.Errorf("%s stating %q was accepted as %q", c.skill, c.stated, n)
		}
	}
	for _, l := range Allowed {
		if strings.HasPrefix(l, "GPL") {
			t.Errorf("GPL licence %s is allowed", l)
		}
	}
	for _, gpl := range []string{"bioservices", "cobrapy", "etetoolkit"} {
		if Cut[gpl] != cutGPL {
			t.Errorf("GPL skill %s is not cut as GPL", gpl)
		}
	}
	for _, un := range []string{"hugging-science", "latex-posters", "pyhealth", "polars", "sympy", "matplotlib"} {
		if _, ok := Licences[un]; Cut[un] != cutUnconfirmed && !ok {
			t.Errorf("%s states no licence of its own text, yet is neither cut nor confirmed by hand", un)
		}
	}
}

func TestTheCurationIsTheSpecs(t *testing.T) {
	// A skill confirmed by hand moves from Cut to Allow: update both numbers.
	if len(Cut) != 51 || len(Allow) != 126 || len(Cut)+len(Allow) != 177 || !slices.IsSorted(Allow) {
		t.Fatalf("%d cut, %d allowed (sorted %v)", len(Cut), len(Allow), slices.IsSorted(Allow))
	}
	for _, a := range Allow {
		if _, cut := Cut[a]; cut {
			t.Errorf("%s is both cut and allowed", a)
		}
	}
	keep := Keep()
	if len(keep["paper-lookup"]) != 16 || len(keep["database-lookup"]) != 58 {
		t.Fatalf("keep %d paper references and %d database references", len(keep["paper-lookup"]), len(keep["database-lookup"]))
	}
	for skill := range keep {
		if Overlays[skill] == "" {
			t.Errorf("%s has no overlay", skill)
		}
	}
}

func TestFrontmatterReadsQuotedAndFoldedValues(t *testing.T) {
	fm, body, err := ParseFrontmatter([]byte("---\nname: x\ndescription: >\n  One line,\n  folded.\nlicense: \"MIT\"\nmetadata:\n  version: \"1\"\n---\n\n# X\n"))
	if err != nil || fm.Name != "x" || fm.Description != "One line, folded.\n" || fm.License != "MIT" || string(body) != "\n# X\n" {
		t.Fatalf("%+v %q %v", fm, body, err)
	}
	for _, bad := range []string{"# no frontmatter\n", "---\nname: x\n", "---\nname: [\n---\n"} {
		if _, _, err := ParseFrontmatter([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
