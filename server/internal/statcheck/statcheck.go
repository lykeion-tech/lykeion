// Package statcheck recomputes the p-value of a reported test statistic, to
// check that a paper's p agrees with its statistic and degrees of freedom.
package statcheck

import (
	"errors"
	"fmt"
	"math"
)

// Test is one reported result such as t(28) = 2.20, p = .04.
type Test struct {
	Kind       string // t, F, chi2, r or z
	Statistic  float64
	StatDigits int // decimals the statistic was reported with
	DF1, DF2   float64
	P          float64
	PDigits    int    // decimals the p was reported with
	PBound     string // "=", "<" or ">"
}

// P is the two-tailed p for t, r and z, and the upper-tail p for F and chi2.
func P(t Test) (float64, error) {
	return pAt(t, t.Statistic)
}

// Consistent reports whether the reported p agrees with the statistic. The
// statistic was rounded to StatDigits decimals, so the true value lies within
// half a unit of the last digit; p is monotone in |statistic|, which gives an
// interval [lo, hi] of possible p values. For "=" the result is consistent when
// that interval meets the range of values that round to the reported p at
// PDigits; for "<" when lo is below the reported p; for ">" when hi is above
// it. The returned float is p at the reported statistic.
func Consistent(t Test) (bool, float64, error) {
	p, err := P(t)
	if err != nil {
		return false, 0, err
	}
	if t.PBound != "=" && t.PBound != "<" && t.PBound != ">" {
		return false, p, fmt.Errorf("statcheck: unknown p bound %q", t.PBound)
	}
	half := 0.5 * math.Pow(10, -float64(t.StatDigits))
	s := math.Abs(t.Statistic)
	lowStat := math.Max(s-half, 0)
	hiStat := s + half
	hi, err := pAt(t, lowStat)
	if err != nil {
		return false, p, err
	}
	lo, err := pAt(t, hiStat)
	if err != nil {
		lo = 0 // a correlation rounded up to 1 or beyond has p -> 0
	}
	switch t.PBound {
	case "<":
		return lo < t.P, p, nil
	case ">":
		return hi > t.P, p, nil
	}
	pHalf := 0.5 * math.Pow(10, -float64(t.PDigits))
	return lo <= t.P+pHalf && hi >= t.P-pHalf, p, nil
}

func pAt(t Test, x float64) (float64, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, errors.New("statcheck: statistic is not a finite number")
	}
	switch t.Kind {
	case "z":
		return math.Erfc(math.Abs(x) / math.Sqrt2), nil
	case "t":
		if !(t.DF1 > 0) {
			return 0, errors.New("statcheck: degrees of freedom must be positive")
		}
		return tTail(x, t.DF1), nil
	case "r":
		if !(t.DF1 > 0) {
			return 0, errors.New("statcheck: degrees of freedom must be positive")
		}
		if math.Abs(x) >= 1 {
			return 0, errors.New("statcheck: |r| must be below 1")
		}
		return tTail(x*math.Sqrt(t.DF1/(1-x*x)), t.DF1), nil
	case "F":
		if !(t.DF1 > 0) || !(t.DF2 > 0) {
			return 0, errors.New("statcheck: degrees of freedom must be positive")
		}
		if x < 0 {
			return 0, errors.New("statcheck: F must not be negative")
		}
		return betaInc(t.DF2/2, t.DF1/2, t.DF2/(t.DF2+t.DF1*x)), nil
	case "chi2":
		if !(t.DF1 > 0) {
			return 0, errors.New("statcheck: degrees of freedom must be positive")
		}
		if x < 0 {
			return 0, errors.New("statcheck: chi2 must not be negative")
		}
		return gammaUpper(t.DF1/2, x/2), nil
	}
	return 0, fmt.Errorf("statcheck: unknown test kind %q", t.Kind)
}

// tTail is the two-tailed p of Student's t: I_{df/(df+t^2)}(df/2, 1/2).
func tTail(x, df float64) float64 {
	return betaInc(df/2, 0.5, df/(df+x*x))
}

const (
	maxIter = 500
	eps     = 1e-15
	tiny    = 1e-300
)

// betaInc is the regularised incomplete beta function I_x(a, b).
func betaInc(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log1p(-x))
	// The fraction converges quickly below the mean; use the symmetry above it.
	if x < (a+1)/(a+b+2) {
		return front * betaFrac(a, b, x) / a
	}
	return 1 - front*betaFrac(b, a, 1-x)/b
}

// betaFrac evaluates 1/(1+d1/(1+d2/(1+...))) with the modified Lentz method.
func betaFrac(a, b, x float64) float64 {
	f, c, d := tiny, tiny, 0.0
	for i := 0; i <= 2*maxIter; i++ {
		var num float64
		if i == 0 {
			num = 1
		} else if m := float64(i / 2); i%2 == 1 {
			num = -(a + m) * (a + b + m) * x / ((a + 2*m) * (a + 2*m + 1))
		} else {
			num = m * (b - m) * x / ((a + 2*m - 1) * (a + 2*m))
		}
		d = 1 + num*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		d = 1 / d
		c = 1 + num/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		f *= c * d
		if math.Abs(c*d-1) < eps && i > 0 {
			break
		}
	}
	return f
}

// gammaUpper is the regularised upper incomplete gamma function Q(a, x).
func gammaUpper(a, x float64) float64 {
	if x <= 0 {
		return 1
	}
	lg, _ := math.Lgamma(a)
	front := math.Exp(a*math.Log(x) - x - lg)
	if x < a+1 {
		// Series for P(a, x) = front * sum x^n / (a (a+1) ... (a+n)).
		term := 1 / a
		sum := term
		for n := 1; n < maxIter*10; n++ {
			term *= x / (a + float64(n))
			sum += term
			if term < sum*eps {
				break
			}
		}
		return 1 - front*sum
	}
	// Continued fraction x+1-a - 1(1-a)/(x+3-a - 2(2-a)/(x+5-a - ...)), Lentz.
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	f := d
	for i := 1; i < maxIter; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := c * d
		f *= delta
		if math.Abs(delta-1) < eps {
			break
		}
	}
	return front * f
}
