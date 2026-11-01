package review

import (
	"strings"
	"testing"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/jev"
)

var _ Asker = (*jev.Client)(nil)

func TestItemStateFitsJevsLimit(t *testing.T) {
	big := strings.Repeat("é", 512<<10)
	got := ItemState("grounding", big, []string{big, big}, MaxState)
	if len(got) > MaxState {
		t.Fatalf("len %d > %d", len(got), MaxState)
	}
	if !strings.Contains(got, "[cut") {
		t.Error("no cut mark")
	}
	if n := strings.Count(got, "-BEGIN\n"); n != 3 || strings.Count(got, "-END\n") != 3 {
		t.Errorf("want 3 fenced pieces, got %d", n)
	}
	if !strings.HasPrefix(got, "Stage: grounding\n") {
		t.Error("stage first")
	}
}

// The cut rule is copied from flow.Excerpt; this pins them together.
func TestExcerptMatchesFlow(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 5, 100} {
		s := strings.Repeat("aé", 10)
		a, ac := excerpt(s, n)
		b, bc := flow.Excerpt(s, n)
		if a != b || ac != bc {
			t.Errorf("n=%d differs", n)
		}
	}
}
