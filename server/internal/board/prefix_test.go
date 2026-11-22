package board

import "testing"

func TestDisplayPrefix(t *testing.T) {
	for _, c := range []struct{ prefix, slug, want string }{
		{"BETA", "smith-lab", "BETA"},
		{"", "smith-lab", "SMIT"},
		{"", "ab-12", "AB"},
		{"", "x1", "LYK"},
		{"", "42-lab", "LAB"},
	} {
		if got := DisplayPrefix(c.prefix, c.slug); got != c.want {
			t.Errorf("DisplayPrefix(%q, %q) = %q, want %q", c.prefix, c.slug, got, c.want)
		}
	}
}
