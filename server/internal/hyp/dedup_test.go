package hyp

import (
	"slices"
	"testing"
)

func TestClusterMergesOnlyTheDuplicate(t *testing.T) {
	same := "X raises Y by inducing Z. X induces Z in liver."
	surv, merged := Cluster([]string{"H1", "H2", "H3"},
		[]string{same, "A batch artefact in the Y assay mimics an effect of X.", same}, DedupThreshold)
	if !slices.Equal(surv, []string{"H1", "H2"}) || len(merged) != 1 || merged["H3"] != "H1" {
		t.Fatalf("%v %v", surv, merged)
	}
	if surv, _ := Cluster([]string{"H1", "H2"}, []string{"", ""}, DedupThreshold); len(surv) != 2 {
		t.Fatal("texts with no words are not duplicates of each other")
	}
}

func TestPickSpreadsSeeds(t *testing.T) {
	got := Pick(3, []string{"g1", "g2"}, []string{"E1", "E2"})
	if !slices.Equal(got, []Seed{{"g1", "E1"}, {"g2", "E2"}, {"g1", "E2"}}) {
		t.Fatalf("%v", got)
	}
	if got := Pick(3, []string{"g"}, []string{"E1", "E2", "E3"}); !slices.Equal(got, []Seed{{"g", "E1"}, {"g", "E2"}, {"g", "E3"}}) {
		t.Fatalf("%v", got)
	}
	if got := Pick(3, []string{"g"}, []string{"E1"}); len(got) != 3 || got[2] != (Seed{"g", "E1"}) {
		t.Fatalf("with one pair every generator shares it: %v", got)
	}
}

func TestGapsReadsTheNotesHeadings(t *testing.T) {
	notes := "## Known approaches\n- DESeq2\n\n## Gaps and open questions\n- whether Z mediates\n* dose dependence\n-\n\n## Best known result\n- not a gap\n"
	if got := Gaps(notes); !slices.Equal(got, []string{"whether Z mediates", "dose dependence"}) {
		t.Fatalf("%q", got)
	}
}

func TestVectorsUseUTF8ByteThreshold(t *testing.T) {
	got := vectors([]string{"a ab é éa 猫 abc"})[0]
	for _, w := range []string{"a", "ab", "é"} {
		if _, ok := got[w]; ok {
			t.Fatalf("short token %q survived", w)
		}
	}
	for _, w := range []string{"éa", "猫", "abc"} {
		if got[w] != 1 {
			t.Fatalf("token %q weight %g", w, got[w])
		}
	}
}
