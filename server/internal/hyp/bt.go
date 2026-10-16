// Package hyp is the Hypotheses column: the records its roles write, the
// steps the server takes between its phases, and its gate.
package hyp

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
)

// Judgment is one pairwise comparison: PA is the probability that A is the
// better candidate to carry forward than B.
type Judgment struct {
	A      string  `json:"a"`
	B      string  `json:"b"`
	PA     float64 `json:"p_a"`
	Judge  string  `json:"judge"` // jev or selector
	Model  string  `json:"model,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

// Fit returns Bradley-Terry strengths for items, summing to 1. It runs the
// minorization-maximization iteration for fractional wins,
// p_i ← W_i / Σ_j n_ij/(p_i+p_j), rescaling each step, until no strength
// moves by more than 1e-12. Each probability is clipped to [0.05, 0.95], so
// a candidate that lost every comparison keeps a little win and the
// strengths stay finite.
func Fit(items []string, js []Judgment) (map[string]float64, error) {
	n := len(items)
	if n == 0 {
		return nil, errors.New("nothing to rank")
	}
	idx := map[string]int{}
	for i, it := range items {
		idx[it] = i
	}
	wins := make([]float64, n)
	count := make([][]float64, n)
	for i := range count {
		count[i] = make([]float64, n)
	}
	for _, j := range js {
		a, okA := idx[j.A]
		b, okB := idx[j.B]
		if !okA || !okB || a == b {
			return nil, fmt.Errorf("the judgment %s against %s names a candidate outside the ranking", j.A, j.B)
		}
		pa := min(max(j.PA, 0.05), 0.95)
		wins[a] += pa
		wins[b] += 1 - pa
		count[a][b]++
		count[b][a]++
	}
	if n == 1 {
		return map[string]float64{items[0]: 1}, nil
	}
	for i, w := range wins {
		if w == 0 {
			return nil, fmt.Errorf("%s was never compared", items[i])
		}
	}
	p := make([]float64, n)
	for i := range p {
		p[i] = 1 / float64(n)
	}
	for range 100000 {
		next := make([]float64, n)
		sum := 0.0
		for i := range p {
			d := 0.0
			for k := range p {
				if count[i][k] > 0 {
					d += count[i][k] / (p[i] + p[k])
				}
			}
			next[i] = wins[i] / d
			sum += next[i]
		}
		moved := 0.0
		for i := range next {
			next[i] /= sum
			moved = max(moved, math.Abs(next[i]-p[i]))
		}
		p = next
		if moved < 1e-12 {
			out := make(map[string]float64, n)
			for i, it := range items {
				out[it] = p[i]
			}
			return out, nil
		}
	}
	return nil, errors.New("the ranking did not converge")
}

// Order lists the scored items strongest first; ties go to the earlier key.
func Order(scores map[string]float64) []string {
	out := slices.Sorted(maps.Keys(scores))
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(scores[b], scores[a]) })
	return out
}
