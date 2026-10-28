package coresearch

import (
	"os"
	"strings"
	"testing"
)

func TestTemplatesShip(t *testing.T) {
	want := []string{"blank", "neurips", "icml", "iclr", "acl", "cvpr", "arxiv", "thesis"}
	labels := []string{"Blank", "NeurIPS", "ICML", "ICLR", "ACL", "CVPR", "arXiv preprint", "Thesis"}
	templates := Templates()
	if len(templates) != len(want) {
		t.Fatalf("templates: %d", len(templates))
	}
	for i, tmpl := range templates {
		if tmpl.ID != want[i] || tmpl.Label != labels[i] || len(tmpl.Files["main.tex"]) == 0 {
			t.Fatalf("template %d %+v", i, tmpl)
		}
		if _, ok := tmpl.Files["refs.bib"]; !ok {
			t.Fatalf("missing bibliography %s", tmpl.ID)
		}
		if tmpl.ID == "acl" || tmpl.ID == "cvpr" {
			for path := range tmpl.Files {
				if strings.HasSuffix(path, ".sty") {
					t.Fatalf("unlicensed style %s", path)
				}
			}
			if !strings.Contains(string(tmpl.Files["main.tex"]), "add the official") {
				t.Fatal("style guidance missing")
			}
		}
		if tmpl.ID == "arxiv" && len(tmpl.Files["arxiv.sty"]) == 0 {
			t.Fatal("arxiv style missing")
		}
	}
	notice, err := os.ReadFile("../../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"arxiv.sty", "acl_natbib.bst", "ieeenat_fullname.bst"} {
		if !strings.Contains(string(notice), name) {
			t.Fatalf("notice missing %s", name)
		}
	}
}
