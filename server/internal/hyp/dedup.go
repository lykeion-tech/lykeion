package hyp

import (
	"math"
	"strings"
	"unicode"
)

// DedupThreshold is the cosine similarity at or above which two candidates
// count as one. It is high on purpose: merging two distinct hypotheses loses
// one, while a missed duplicate only costs a comparison.
const DedupThreshold = 0.9

// Cluster keeps the first of every group of near-identical texts, comparing
// TF-IDF vectors by cosine. A text at or above threshold against an earlier
// survivor merges into it.
// ponytail: lexical vectors only catch rewordings that share words; swap in
// an embedding model once one is reachable without new egress.
func Cluster(keys, texts []string, threshold float64) ([]string, map[string]string) {
	vs := vectors(texts)
	survivors, merged := []string{}, map[string]string{}
	var kept []int
	for i := range texts {
		into := -1
		for _, k := range kept {
			if cosine(vs[i], vs[k]) >= threshold {
				into = k
				break
			}
		}
		if into >= 0 {
			merged[keys[i]] = keys[into]
			continue
		}
		kept = append(kept, i)
		survivors = append(survivors, keys[i])
	}
	return survivors, merged
}

// vectors weighs each word of three or more letters or digits by its count
// times a smoothed inverse document frequency, which is never zero.
func vectors(texts []string) []map[string]float64 {
	tf := make([]map[string]float64, len(texts))
	df := map[string]int{}
	for i, t := range texts {
		tf[i] = map[string]float64{}
		for _, w := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			if len(w) < 3 {
				continue
			}
			if tf[i][w] == 0 {
				df[w]++
			}
			tf[i][w]++
		}
	}
	n := float64(len(texts))
	for i := range tf {
		for w, f := range tf[i] {
			tf[i][w] = f * (math.Log((1+n)/(1+float64(df[w]))) + 1)
		}
	}
	return tf
}

func cosine(a, b map[string]float64) float64 {
	var dot, na, nb float64
	for w, x := range a {
		dot += x * b[w]
		na += x * x
	}
	for _, y := range b {
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// Seed is what one generator starts from: a gap Grounding left open and one
// Grounding claim to draw on.
type Seed struct {
	Gap   string `json:"gap"`
	Claim string `json:"claim"`
}

// Pick chooses n seeds from gaps × claims: each time the unused pair whose gap
// and claim have been used least, the earlier pair on a tie. Pairs repeat only
// once every pair is taken.
func Pick(n int, gaps, claims []string) []Seed {
	used := map[Seed]bool{}
	ug, uc := make([]int, len(gaps)), make([]int, len(claims))
	var out []Seed
	for len(out) < n && len(gaps) > 0 && len(claims) > 0 {
		best, bi, bj := -1, 0, 0
		for i := range gaps {
			for j := range claims {
				if used[Seed{gaps[i], claims[j]}] && len(used) < len(gaps)*len(claims) {
					continue
				}
				if score := ug[i] + uc[j]; best < 0 || score < best {
					best, bi, bj = score, i, j
				}
			}
		}
		s := Seed{gaps[bi], claims[bj]}
		used[s] = true
		ug[bi]++
		uc[bj]++
		out = append(out, s)
	}
	return out
}

// Gaps lists the bullet items under the notes' headings that mention gaps or
// open questions.
func Gaps(notes string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(notes, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			h := strings.ToLower(t)
			in = strings.Contains(h, "gap") || strings.Contains(h, "open question")
			continue
		}
		if in && (strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*")) {
			if g := strings.TrimSpace(t[1:]); g != "" {
				out = append(out, g)
			}
		}
	}
	return out
}

// interleave alternates two lists, so seeds draw on both supporting and
// challenging claims.
func interleave(a, b []string) []string {
	var out []string
	for i := 0; i < len(a) || i < len(b); i++ {
		if i < len(a) {
			out = append(out, a[i])
		}
		if i < len(b) {
			out = append(out, b[i])
		}
	}
	return out
}
