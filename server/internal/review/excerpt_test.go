package review_test

import (
	"strings"
	"testing"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/review"
)

// The cut rule is copied from flow.Excerpt; this pins them together.
func TestExcerptMatchesFlow(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 5, 100} {
		s := strings.Repeat("aé", 10)
		a, ac := review.Excerpt(s, n)
		b, bc := flow.Excerpt(s, n)
		if a != b || ac != bc {
			t.Errorf("n=%d differs", n)
		}
	}
}
