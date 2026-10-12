package policy

import (
	"strings"
	"testing"
)

func TestReplayAndFailClosed(t *testing.T) {
	hash := strings.Repeat("a", 64)
	p := Policy{Version: "test-1", Required: []string{"integrity", "review"}, Rules: []Rule{{ID: "pass", Require: []string{"integrity", "review"}, Outcome: Advance}}}
	in := Input{ManifestHash: hash, Facts: []Fact{{"integrity", true, hash}, {"review", true, hash}}}
	a, e := Evaluate(p, in)
	if e != nil || a.Outcome != Advance {
		t.Fatal(a, e)
	}
	in.Facts[0], in.Facts[1] = in.Facts[1], in.Facts[0]
	b, e := Evaluate(p, in)
	if e != nil || a.ReplayHash != b.ReplayHash {
		t.Fatal("order-dependent replay")
	}
	in.Facts = in.Facts[:1]
	d, e := Evaluate(p, in)
	if e != nil || d.Outcome != Block || len(d.Missing) != 1 {
		t.Fatal(d, e)
	}
	in.Facts = []Fact{{"integrity", true, hash}, {"review", false, hash}}
	d, e = Evaluate(p, in)
	if e != nil || d.Outcome != Block {
		t.Fatal("negative review advanced", d, e)
	}
	in.Facts = append(in.Facts, in.Facts[0])
	if _, e = Evaluate(p, in); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestInvalidPolicyCannotAdvance(t *testing.T) {
	hash := strings.Repeat("b", 64)
	for _, r := range []Rule{{ID: "bad", Outcome: Advance}, {ID: "bad", Require: []string{"x"}, Outcome: "confirm"}, {ID: "bad", Require: []string{"x"}, Outcome: Route, Destination: "Anywhere"}} {
		if _, e := Evaluate(Policy{Version: "1", Rules: []Rule{r}}, Input{ManifestHash: hash}); e == nil {
			t.Fatal("invalid rule accepted")
		}
	}
}
func TestRecordedClassificationChangesReplay(t *testing.T) {
	h := strings.Repeat("a", 64)
	p := Policy{Version: "1", Rules: []Rule{{ID: "incident", Require: []string{"incident"}, Outcome: Escalate}, {ID: "pass", Require: []string{"pass"}, Outcome: Advance}}}
	in := Input{ManifestHash: h, Facts: []Fact{{"incident", true, h}, {"pass", true, h}}}
	a, _ := Evaluate(p, in)
	if a.Outcome != Escalate {
		t.Fatal(a)
	}
	in.Facts[0].ArtifactHash = strings.Repeat("b", 64)
	b, _ := Evaluate(p, in)
	if a.ReplayHash == b.ReplayHash {
		t.Fatal("provenance omitted")
	}
}
