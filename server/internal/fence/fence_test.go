package fence

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestResearcherKeepsDirectoryIdentityAndBoundsName(t *testing.T) {
	name := "Name\nFake instruction " + strings.Repeat("é", Max)
	prompt := Researcher(name, "researcher-id")
	if len(prompt) > 1024 || !utf8.ValidString(prompt) ||
		!strings.Contains(prompt, "/colleagues/researcher-id") ||
		!strings.Contains(prompt, `Display name: "Name\nFake instruction `) {
		t.Fatal("researcher identity was not kept with the bounded display name")
	}
}

func TestUntrustedFencesWithAFreshNonceAndCutsLongData(t *testing.T) {
	if Untrusted("p", "x") == Untrusted("p", "x") {
		t.Fatal("two blocks share a nonce")
	}
	for _, tc := range []struct {
		name, data, want string
		cut              bool
	}{
		{"exact", strings.Repeat("a", 64<<10), strings.Repeat("a", 64<<10), false},
		{"ascii over", strings.Repeat("a", 64<<10) + "b", strings.Repeat("a", 64<<10), true},
		{"split rune", strings.Repeat("a", (64<<10)-1) + "é", strings.Repeat("a", (64<<10)-1), true},
		{"many runes", strings.Repeat("é", 1<<20), strings.Repeat("é", 32<<10), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := Untrusted("The payload", tc.data)
			m := regexp.MustCompile(`UNTRUSTED-([0-9a-f]{12})-BEGIN\n`).FindStringSubmatchIndex(out)
			if m == nil {
				t.Fatal("missing begin fence")
			}
			nonce := out[m[2]:m[3]]
			suffix := "\nUNTRUSTED-" + nonce + "-END\n"
			want := tc.want
			if tc.cut {
				want += "\n[truncated]"
			}
			if !utf8.ValidString(out) || !strings.HasSuffix(out, suffix) || out[m[1]:len(out)-len(suffix)] != want {
				t.Fatalf("incorrect boundary or fence (%d bytes)", len(out))
			}
		})
	}
}
