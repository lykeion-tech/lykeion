package statcheck

import (
	"math"
	"testing"
)

func TestPReferenceValues(t *testing.T) {
	cases := []struct {
		name string
		in   Test
		want float64
	}{
		{"t", Test{Kind: "t", Statistic: 2.20, DF1: 28}, 0.0362},
		{"F", Test{Kind: "F", Statistic: 3.32, DF1: 2, DF2: 30}, 0.0498},
		{"chi2", Test{Kind: "chi2", Statistic: 3.84, DF1: 1}, 0.0500},
		{"r", Test{Kind: "r", Statistic: 0.30, DF1: 48}, 0.0343},
		{"z", Test{Kind: "z", Statistic: 1.96}, 0.0500},
		{"t negative", Test{Kind: "t", Statistic: -2.20, DF1: 28}, 0.0362},
	}
	for _, c := range cases {
		got, err := P(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if math.Abs(got-c.want) > 1e-4 {
			t.Errorf("%s: p = %.6f, want %.4f", c.name, got, c.want)
		}
	}
}

func TestPEdgeCases(t *testing.T) {
	bad := []Test{
		{Kind: "t", Statistic: 2, DF1: 0},
		{Kind: "t", Statistic: 2, DF1: -3},
		{Kind: "F", Statistic: 2, DF1: 2, DF2: 0},
		{Kind: "chi2", Statistic: 2, DF1: 0},
		{Kind: "r", Statistic: 1, DF1: 10},
		{Kind: "r", Statistic: -1.2, DF1: 10},
		{Kind: "t", Statistic: math.NaN(), DF1: 10},
		{Kind: "z", Statistic: math.NaN()},
		{Kind: "q", Statistic: 1, DF1: 10},
	}
	for _, b := range bad {
		if _, err := P(b); err == nil {
			t.Errorf("%+v: want error", b)
		}
	}
}

func TestConsistent(t *testing.T) {
	base := Test{Kind: "t", Statistic: 2.20, StatDigits: 2, DF1: 28}
	cases := []struct {
		name  string
		p     float64
		pd    int
		bound string
		want  bool
	}{
		{"p=.04", 0.04, 2, "=", true},
		{"p<.05", 0.05, 2, "<", true},
		{"p=.01", 0.01, 2, "=", false},
		{"p<.01", 0.01, 2, "<", false},
		{"p>.05", 0.05, 2, ">", false},
		{"p>.03", 0.03, 2, ">", true},
	}
	for _, c := range cases {
		in := base
		in.P, in.PDigits, in.PBound = c.p, c.pd, c.bound
		got, p, err := Consistent(in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: consistent = %v, want %v", c.name, got, c.want)
		}
		if math.Abs(p-0.0362) > 1e-4 {
			t.Errorf("%s: recomputed p = %v", c.name, p)
		}
	}
	// A statistic rounded to one decimal covers a wider range, so p=.03 fits t(28)=2.2.
	in := Test{Kind: "t", Statistic: 2.2, StatDigits: 1, DF1: 28, P: 0.03, PDigits: 2, PBound: "="}
	if ok, _, _ := Consistent(in); !ok {
		t.Error("rounding range should allow p=.03")
	}
	if _, _, err := Consistent(Test{Kind: "t", Statistic: 2, DF1: 10, P: .1, PBound: "~"}); err == nil {
		t.Error("unknown bound should error")
	}
}
