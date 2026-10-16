package hyp

import (
	"math"
	"slices"
	"testing"
)

func TestFitMatchesAHandComputedExample(t *testing.T) {
	// Strengths 4:2:1 give P(A>B) = 4/6, P(A>C) = 4/5 and P(B>C) = 2/3.
	// With exactly those probabilities as fractional wins, the likelihood
	// equations W_i = Σ_j p_i/(p_i+p_j) hold at (4/7, 2/7, 1/7):
	// A: 2/3 + 4/5 = 22/15 on both sides; B: 1/3 + 2/3 = 1; C: 1/5 + 1/3 = 8/15.
	// The C-against-B judgment is written from C's side to pin orientation.
	js := []Judgment{{A: "A", B: "B", PA: 2.0 / 3}, {A: "A", B: "C", PA: 4.0 / 5}, {A: "C", B: "B", PA: 1.0 / 3}}
	got, err := Fit([]string{"A", "B", "C"}, js)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for k, want := range map[string]float64{"A": 4.0 / 7, "B": 2.0 / 7, "C": 1.0 / 7} {
		sum += got[k]
		if math.IsNaN(got[k]) || math.IsInf(got[k], 0) || math.Abs(got[k]-want) > 1e-9 {
			t.Errorf("%s = %.12f, want %.12f", k, got[k], want)
		}
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("score sum = %g", sum)
	}
	if o := Order(got); !slices.Equal(o, []string{"A", "B", "C"}) {
		t.Fatalf("order %v", o)
	}
}

func TestACandidateThatLostEveryPairStaysFiniteAndLast(t *testing.T) {
	js := []Judgment{{A: "A", B: "C", PA: 1}, {A: "B", B: "C", PA: 1}, {A: "A", B: "B", PA: 0.5}}
	got, err := Fit([]string{"A", "B", "C"}, js)
	if err != nil || got["C"] <= 0 || math.IsNaN(got["C"]) || math.Abs(got["A"]-got["B"]) > 1e-9 {
		t.Fatalf("%v %v", got, err)
	}
	if o := Order(got); !slices.Equal(o, []string{"A", "B", "C"}) {
		t.Fatalf("ties go to the earlier key and the loser is last: %v", o)
	}
}

func TestFitRefusesWhatItCannotRank(t *testing.T) {
	if _, err := Fit([]string{"A", "B", "C"}, []Judgment{{A: "A", B: "B", PA: 0.6}}); err == nil {
		t.Fatal("C was never compared")
	}
	if _, err := Fit([]string{"A", "B"}, []Judgment{{A: "A", B: "Z", PA: 0.6}}); err == nil {
		t.Fatal("Z is not being ranked")
	}
	if s, err := Fit([]string{"A"}, nil); err != nil || s["A"] != 1 {
		t.Fatalf("one candidate: %v %v", s, err)
	}
}
